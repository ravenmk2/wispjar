package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"

	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// question 提问工具（runtime.md §4.9）：根 Agent 向用户提问。
// 等待语义 = DSH timed + continued 简化版：execute 内按 callID 登记内存等待，
// 窗口内作答 → 答案作为工具结果返回；超时 → {"pending":true,...} 工具结果，turn 正常
// 继续，问题转 continued（无 question/answered 即未决，投影天然推导，重启零成本）；
// 迟到答复由 REST 合成 user/message 开新 turn。不复用审批的 interrupt/resume。

const defaultQuestionWaitTimeout = 10 * time.Minute

// questionPendingNotice 超时工具结果的 message（措辞对齐调研 §6：是 pending 不是跳过）。
const questionPendingNotice = "The user has not answered within the wait window. This question is PENDING, not skipped: " +
	"the user's answer will arrive later as a new user message (kind: answer_to_pending_question). " +
	"Do NOT treat the absence of an answer as consent, approval, or a choice; " +
	"continue with work that does not depend on these answers, or stop and wait for the user's reply."

// delegatedCallerNotice 子 Agent 调用拒绝文案（对齐 DSH DELEGATED_CALLER）。
const delegatedCallerNotice = "error: question is only available to the root agent; a delegated subagent cannot ask the user directly. " +
	"Write the unresolved questions into your final result so the parent agent can surface them to the user."

var (
	ErrQuestionNotFound   = &Error{Status: http.StatusNotFound, Code: "question/not_found", Message: "提问不存在"}
	ErrQuestionNotPending = &Error{Status: http.StatusConflict, Code: "question/not_pending", Message: "提问已作答或不存在"}
)

// questionDelivery 是一次 open 状态作答的投递载体（by 供 question/answered 落库）。
type questionDelivery struct {
	answers []session.QuestionAnswer
	by      string
}

// pendingQuestion 是 open 状态提问的内存等待（continued 状态无内存态，由事件投影推导）。
type pendingQuestion struct {
	sessionID string
	ch        chan questionDelivery
}

func (o *orchestrator) registerQuestionWait(callID string, w *pendingQuestion) {
	o.mu.Lock()
	o.questions[callID] = w
	o.mu.Unlock()
}

func (o *orchestrator) deregisterQuestionWait(callID string, w *pendingQuestion) {
	o.mu.Lock()
	if o.questions[callID] == w {
		delete(o.questions, callID)
	}
	o.mu.Unlock()
}

// AnswerQuestion 实现 Orchestrator 接口：open 状态投递内存 channel（true），
// question/answered 由等待中的工具在收到投递后落库（保证 asked 先于 answered）；
// 无内存等待返回 false，由调用方走 continued 路径（合成 user message + 新 turn）。
func (o *orchestrator) AnswerQuestion(sessionID, callID string, answers []session.QuestionAnswer, by string) (bool, error) {
	o.mu.Lock()
	w, ok := o.questions[callID]
	if ok && w.sessionID == sessionID {
		delete(o.questions, callID)
	} else {
		ok = false
	}
	o.mu.Unlock()
	if !ok {
		return false, nil
	}
	// channel 缓冲为 1 且等待已从 map 摘除：此处投递必然成功，等待方必然收到。
	w.ch <- questionDelivery{answers: answers, by: by}
	return true, nil
}

type askQuestionOption struct {
	Label       string `json:"label" jsonschema:"description=option label; the answer echoes this label back"`
	Description string `json:"description,omitempty" jsonschema:"description=short helper text for the option"`
}

type askQuestion struct {
	ID          string              `json:"id" jsonschema:"description=stable question id, unique within this call; echoed back in the answer"`
	Question    string              `json:"question" jsonschema:"description=the question text shown to the user"`
	Header      string              `json:"header,omitempty" jsonschema:"description=short title chip, e.g. Confirm or Choose Mode"`
	Options     []askQuestionOption `json:"options,omitempty" jsonschema:"description=choice list; omit for a free-text question"`
	MultiSelect bool                `json:"multi_select,omitempty" jsonschema:"description=true allows selecting multiple options; default single choice"`
}

type askUserQuestionArgs struct {
	Questions []askQuestion `json:"questions" jsonschema:"description=questions to ask the user, presented as one card"`
}

const askUserQuestionDescription = `Ask the user one or more questions and wait for the answers (up to 10 minutes).
Each question needs a stable id and the question text; optionally a short header.
With options the user picks by label (multi_select=true allows several); without options the question is free-text.
The tool result is {"answers":[{"id","selected":[label...],"custom"?}]} — one entry per question; an entry with empty selected and no custom means the user skipped that question.
If the user does not answer in time the result is {"pending":true,...}: the question stays answerable and the late answer arrives as a new user message — never treat it as consent.
Use this when you genuinely need the user's decision or missing information, not for rhetorical confirmation.`

// questionTool 仅 depth=0 根 Agent 可真正提问；depth=1（task 子 Agent）注册同 schema
// 的拒绝桩：子 Agent 直接调用未注册工具会被 eino 升级为整个子运行失败，桩把
// DELEGATED_CALLER 语义以工具结果文本回喂，子 Agent 可自行把问题上浮到最终结果。
type questionTool struct {
	o     *orchestrator
	sess  *store.Session
	rs    *runState
	depth int
}

func (o *orchestrator) newQuestionTool(sess *store.Session, rs *runState, depth int) (tool.BaseTool, error) {
	qt := &questionTool{o: o, sess: sess, rs: rs, depth: depth}
	return utils.InferTool("question", askUserQuestionDescription, qt.invoke)
}

func (t *questionTool) invoke(ctx context.Context, in askUserQuestionArgs) (string, error) {
	if tc := tools.FromContext(ctx); tc != nil {
		meta := tc.BeginMeta(ctx, tools.RenderGeneric)
		start := time.Now()
		defer func() { meta.DurationMS = time.Since(start).Milliseconds() }()
	}
	if t.depth > 0 {
		return delegatedCallerNotice, nil
	}
	questions, err := normalizeQuestions(in.Questions)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	callID := compose.GetToolCallID(ctx)
	// 先登记等待再落 asked：保证 answered（收到投递后由本工具落库）恒在 asked 之后。
	w := &pendingQuestion{sessionID: t.sess.ID, ch: make(chan questionDelivery, 1)}
	t.o.registerQuestionWait(callID, w)
	defer t.o.deregisterQuestionWait(callID, w)
	if _, err := t.o.commit(context.Background(), t.sess.ID, []session.EventInput{
		{Type: session.EventQuestionAsked, Data: session.QuestionAskedData{
			Turn: t.rs.turn, Step: t.rs.currentStep(), CallID: callID, Questions: questions,
		}},
	}); err != nil {
		return "", fmt.Errorf("question/asked 落库失败: %w", err)
	}

	timer := time.NewTimer(t.o.questionTimeout)
	defer timer.Stop()
	answered := func(d questionDelivery) (string, error) {
		if _, err := t.o.commit(context.Background(), t.sess.ID, []session.EventInput{
			{Type: session.EventQuestionAnswered, Data: session.QuestionAnsweredData{
				CallID: callID, Answers: d.answers, AnsweredBy: d.by,
			}},
		}); err != nil {
			return "", fmt.Errorf("question/answered 落库失败: %w", err)
		}
		return marshalQuestionResult(map[string]any{"answers": d.answers})
	}
	select {
	case d := <-w.ch:
		return answered(d)
	case <-timer.C:
		// 超时不是失败：turn 正常继续，问题转 continued 保持可答。
		// 与 AnswerQuestion 摘除等待存在竞态：已投递的答案不丢，按作答收尾。
		select {
		case d := <-w.ch:
			return answered(d)
		default:
		}
		return marshalQuestionResult(map[string]any{
			"pending": true, "call_id": callID, "message": questionPendingNotice,
		})
	case <-ctx.Done():
		// turn 取消/中断：等待随 ctx 结束；已投递的答案仍补记 answered（不丢用户作答），
		// 未答的问题无 question/answered，投影上仍属 continued 可补答。
		select {
		case d := <-w.ch:
			_, _ = t.o.commit(context.Background(), t.sess.ID, []session.EventInput{
				{Type: session.EventQuestionAnswered, Data: session.QuestionAnsweredData{
					CallID: callID, Answers: d.answers, AnsweredBy: d.by,
				}},
			})
		default:
		}
		return "", ctx.Err()
	}
}

func marshalQuestionResult(v map[string]any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// normalizeQuestions 校验并转换模型入参；错误以工具结果文本回喂（模型可修正重试）。
func normalizeQuestions(in []askQuestion) ([]session.Question, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("questions must contain at least one question")
	}
	seen := map[string]bool{}
	out := make([]session.Question, 0, len(in))
	for _, q := range in {
		if q.ID == "" {
			return nil, fmt.Errorf("each question needs a non-empty id")
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("question id %q is duplicated within this call", q.ID)
		}
		seen[q.ID] = true
		if q.Question == "" {
			return nil, fmt.Errorf("question %q needs a non-empty question text", q.ID)
		}
		opts := make([]session.QuestionOption, 0, len(q.Options))
		for _, op := range q.Options {
			if op.Label == "" {
				return nil, fmt.Errorf("question %q has an option with an empty label", q.ID)
			}
			opts = append(opts, session.QuestionOption{Label: op.Label, Description: op.Description})
		}
		out = append(out, session.Question{
			ID: q.ID, Question: q.Question, Header: q.Header,
			Options: opts, MultiSelect: q.MultiSelect,
		})
	}
	return out, nil
}

// validateQuestionAnswers REST 侧校验（question/bad_answer）：每题恰好一条答案，
// selected 必须是该题选项 label；单选至多一个 selected；自由输入题不允许 selected。
func validateQuestionAnswers(questions []session.Question, answers []session.QuestionAnswer) error {
	if len(answers) != len(questions) {
		return fmt.Errorf("want exactly %d answers (one per question), got %d", len(questions), len(answers))
	}
	seen := map[string]bool{}
	for _, a := range answers {
		if seen[a.ID] {
			return fmt.Errorf("question %q answered more than once", a.ID)
		}
		seen[a.ID] = true
	}
	for _, q := range questions {
		var a *session.QuestionAnswer
		for i := range answers {
			if answers[i].ID == q.ID {
				a = &answers[i]
				break
			}
		}
		if a == nil {
			return fmt.Errorf("question %q has no answer", q.ID)
		}
		if len(q.Options) == 0 {
			if len(a.Selected) > 0 {
				return fmt.Errorf("question %q is free-text; selected must be empty", q.ID)
			}
			continue
		}
		if !q.MultiSelect && len(a.Selected) > 1 {
			return fmt.Errorf("question %q is single-choice; want at most one selected label", q.ID)
		}
		labels := map[string]bool{}
		for _, op := range q.Options {
			labels[op.Label] = true
		}
		for _, s := range a.Selected {
			if !labels[s] {
				return fmt.Errorf("question %q: %q is not one of its option labels", q.ID, s)
			}
		}
	}
	return nil
}
