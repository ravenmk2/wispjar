import { create } from "zustand"

// 单题作答草稿：selected 回传选项 label；custom 自由文本；skipped 显式跳过
export interface QuestionAnswerDraft {
  selected: string[]
  custom: string
  skipped: boolean
}

interface QuestionCardDraft {
  index: number
  answers: Record<string, QuestionAnswerDraft>
}

interface QuestionDraftState {
  // key = `${sessionId}:${callId}`，卡片重开/切换会话后恢复进度
  drafts: Record<string, QuestionCardDraft>
  setIndex: (key: string, index: number) => void
  setAnswer: (key: string, questionId: string, answer: QuestionAnswerDraft) => void
  clear: (key: string) => void
}

export function questionDraftKey(sessionId: string, callId: string): string {
  return `${sessionId}:${callId}`
}

export const EMPTY_ANSWER: QuestionAnswerDraft = { selected: [], custom: "", skipped: false }

export const useQuestionDraftStore = create<QuestionDraftState>()((set) => ({
  drafts: {},
  setIndex: (key, index) =>
    set((state) => {
      const draft = state.drafts[key] ?? { index: 0, answers: {} }
      return { drafts: { ...state.drafts, [key]: { ...draft, index } } }
    }),
  setAnswer: (key, questionId, answer) =>
    set((state) => {
      const draft = state.drafts[key] ?? { index: 0, answers: {} }
      return {
        drafts: { ...state.drafts, [key]: { ...draft, answers: { ...draft.answers, [questionId]: answer } } },
      }
    }),
  clear: (key) =>
    set((state) => {
      if (!(key in state.drafts)) return state
      const drafts = { ...state.drafts }
      delete drafts[key]
      return { drafts }
    }),
}))
