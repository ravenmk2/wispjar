import { createBrowserRouter, Navigate, Outlet, useLocation } from "react-router"
import { AdminLayout, PlatformLayout } from "@/layouts/AdminLayout"
import WorkspaceLayout from "@/layouts/WorkspaceLayout"
import ChangePasswordPage from "@/pages/ChangePassword"
import LoginPage from "@/pages/Login"
import WorkspaceHome from "@/pages/WorkspaceHome"
import ChatView from "@/features/chat/ChatView"
import MembersPage from "@/features/admin/MembersPage"
import AgentsPage from "@/features/admin/AgentsPage"
import AgentEditorPage from "@/features/admin/AgentEditor"
import SkillsPage from "@/features/admin/SkillsPage"
import OrgUsagePage from "@/features/admin/OrgUsagePage"
import ModelsPage from "@/platform/ModelsPage"
import OrgsPage from "@/platform/OrgsPage"
import ProvidersPage from "@/platform/ProvidersPage"
import SettingsPage from "@/platform/SettingsPage"
import UsersPage from "@/platform/UsersPage"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

function RequireAuth() {
  const token = useIdentityStore((s) => s.token)
  const user = useIdentityStore((s) => s.user)
  const location = useLocation()
  if (!token) return <Navigate to="/login" replace state={{ from: location.pathname }} />
  if (user?.must_change_password) return <Navigate to="/change-password" replace />
  return <Outlet />
}

function RequireOrgAdmin() {
  const currentOrg = useCurrentOrg()
  if (currentOrg?.role !== "admin") return <Navigate to="/" replace />
  return <Outlet />
}

function RequirePlatformAdmin() {
  const currentOrg = useCurrentOrg()
  if (!(currentOrg?.is_platform && currentOrg.role === "admin")) {
    return <Navigate to="/" replace />
  }
  return <Outlet />
}

export const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  { path: "/change-password", element: <ChangePasswordPage /> },
  {
    element: <RequireAuth />,
    children: [
      {
        path: "/",
        element: <WorkspaceLayout />,
        children: [
          { index: true, element: <WorkspaceHome /> },
          { path: "c/:sessionId", element: <ChatView /> },
        ],
      },
      {
        element: <RequireOrgAdmin />,
        children: [
          {
            path: "/admin",
            element: <AdminLayout />,
            children: [
              { index: true, element: <Navigate to="/admin/agents" replace /> },
              { path: "agents", element: <AgentsPage /> },
              { path: "agents/new", element: <AgentEditorPage /> },
              { path: "agents/:id", element: <AgentEditorPage /> },
              { path: "skills", element: <SkillsPage /> },
              { path: "members", element: <MembersPage /> },
              { path: "usage", element: <OrgUsagePage /> },
            ],
          },
        ],
      },
      {
        element: <RequirePlatformAdmin />,
        children: [
          {
            path: "/platform",
            element: <PlatformLayout />,
            children: [
              { index: true, element: <Navigate to="/platform/orgs" replace /> },
              { path: "orgs", element: <OrgsPage /> },
              { path: "users", element: <UsersPage /> },
              { path: "providers", element: <ProvidersPage /> },
              { path: "models", element: <ModelsPage /> },
              { path: "settings", element: <SettingsPage /> },
            ],
          },
        ],
      },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
])
