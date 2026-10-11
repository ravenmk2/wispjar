// 工具名中文映射；含后端改名前的新旧名兼容（question←ask_user_question，skill←activate_skill）
const TOOL_LABELS: Record<string, string> = {
  exec: "执行",
  read: "读取",
  write: "写入",
  edit: "编辑",
  glob: "查找",
  grep: "搜索",
  read_media: "查看",
  skill: "技能",
  activate_skill: "技能",
  task: "任务",
  question: "提问",
  ask_user_question: "提问",
}

export function toolDisplayName(name: string): string {
  return TOOL_LABELS[name] ?? name
}

// 提问工具的新旧名判定（question/asked 事件建节点时可能只知名称之一）
export function isQuestionTool(name: string): boolean {
  return name === "question" || name === "ask_user_question"
}
