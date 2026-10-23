import type { UserOrg } from "./types"

export type MainSection = "workspace" | "personal" | "organization" | "system"

export function mainSection(pathname: string): MainSection {
  if (pathname === "/personal" || pathname.startsWith("/personal/")) return "personal"
  if (pathname === "/admin" || pathname.startsWith("/admin/")) return "organization"
  if (pathname === "/platform" || pathname.startsWith("/platform/")) return "system"
  return "workspace"
}

export const sectionNavigation = {
  personal: {
    title: "个人中心",
    groups: [
      { label: "个人资源", items: [
        { to: "/personal/computers", label: "云电脑", icon: "computer" },
        { to: "/personal/agents", label: "智能体", icon: "agent" },
        { to: "/personal/storage", label: "存储", icon: "storage" },
      ] },
      { label: "账号设置", items: [
        { to: "/personal/account", label: "账号与安全", icon: "account" },
        { to: "/personal/appearance", label: "外观偏好", icon: "appearance" },
      ] },
    ],
  },
  organization: {
    title: "组织管理",
    groups: [{ label: "", items: [
      { to: "/admin/agents", label: "智能体", icon: "agent" },
      { to: "/admin/skills", label: "技能包", icon: "skill" },
      { to: "/admin/members", label: "成员与角色", icon: "members" },
      { to: "/admin/usage", label: "组织用量", icon: "usage" },
    ] }],
  },
  system: {
    title: "系统管理",
    groups: [
      { label: "", items: [
        { to: "/platform/orgs", label: "组织", icon: "organization" },
        { to: "/platform/users", label: "用户", icon: "members" },
      ] },
      { label: "模型服务", items: [
        { to: "/platform/providers", label: "模型供应商", icon: "provider" },
        { to: "/platform/models", label: "模型", icon: "model" },
      ] },
      { label: "运行与维护", items: [
        { to: "/platform/web-search", label: "网络搜索", icon: "search" },
        { to: "/platform/usage", label: "全局用量", icon: "usage" },
        { to: "/platform/audit", label: "操作审计", icon: "audit" },
        { to: "/platform/settings", label: "系统配置", icon: "settings" },
      ] },
    ],
  },
}

export function platformOrganization(orgs: UserOrg[]): UserOrg | undefined {
  return orgs.find((org) => org.is_platform && org.role === "admin")
}

// Changing organization must never retain a chat ID from the previous tenant.
export function orgSwitchPath(pathname: string, org: UserOrg): string {
  const section = mainSection(pathname)
  if (section === "personal") return pathname
  if (section === "organization" && org.role === "admin") return "/admin/agents"
  if (section === "system" && org.is_platform && org.role === "admin") return "/platform/orgs"
  return "/"
}

export function workspaceLocationKey(userId: string, orgId: string): string {
  return `wispjar-workspace-location:${userId}:${orgId}`
}

export function safeWorkspaceLocation(value: string | null): string {
  return value && (/^\/c\/[^/?#]+(?:\?[^#]*)?$/.test(value) || value === "/") ? value : "/"
}
