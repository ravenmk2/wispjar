package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway/ws"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/search"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/skill"
	"github.com/ravenmk2/wispjar/internal/tenant"
	"github.com/ravenmk2/wispjar/internal/usage"
)

type Deps struct {
	Config  *config.Config
	DB      *gorm.DB
	Auth    *auth.Service
	Runtime *runtime.Handler
	WS      *ws.Server
	// WSBus 与 RuntimeSessions 用于 PATCH 附带事件的 follow/control 广播挂钩（可选）。
	WSBus           *runtime.Bus
	RuntimeSessions runtime.SessionRepo
	Computers       *computer.Service
}

func NewRouter(deps Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	v1.POST("/auth/login", deps.Auth.Login)

	authed := v1.Group("", deps.Auth.AuthRequired())
	if deps.WS != nil {
		authed.POST("/auth/ws-ticket", deps.WS.IssueTicket)
	}
	authed.POST("/auth/change-password", deps.Auth.ChangePassword)
	authed.GET("/auth/me", deps.Auth.Me)

	tenantH := tenant.NewHandler(tenant.NewService(deps.DB))
	providerH := provider.NewHandler(provider.NewService(deps.DB, deps.Config.MasterKey()))
	searchH := search.NewHandler(search.NewService(deps.DB, deps.Config.MasterKey()))
	agentSvc := agent.NewService(deps.DB)
	sessionSvc := session.NewService(deps.DB, agentSvc)
	if deps.Computers != nil {
		sessionSvc.SetComputers(deps.Computers)
	}
	if deps.Runtime != nil && deps.WSBus != nil {
		sessionSvc.AfterEvents = func(sessionID string, events []session.Event) {
			for _, ev := range events {
				deps.WSBus.Publish(sessionID, runtime.EventFrame(ev))
			}
			go runtime.PublishControlProjection(deps.RuntimeSessions, deps.WSBus, sessionID)
		}
	}
	sessionH := session.NewHandler(sessionSvc)
	agentH := agent.NewHandler(agentSvc)
	attachmentH := attachment.NewHandler(attachment.NewService(deps.DB, deps.Config.Storage.DataDir), sessionSvc)

	authed.GET("/sessions/:id", sessionH.GetSession)
	authed.PATCH("/sessions/:id", sessionH.PatchSession)
	authed.POST("/sessions/:id/fork", sessionH.ForkSession)
	authed.POST("/sessions/:id/attachments", attachmentH.Upload)
	authed.GET("/attachments/:id", attachmentH.Download)
	if deps.Runtime != nil {
		v1.GET("/previews/:token/files/*path", deps.Auth.PreviewRequired(deps.Runtime.PreviewOwner), deps.Runtime.PreviewContent)
		v1.HEAD("/previews/:token/files/*path", deps.Auth.PreviewRequired(deps.Runtime.PreviewOwner), deps.Runtime.PreviewContent)
		authed.POST("/sessions/:id/previews", deps.Runtime.CreatePreview)
		authed.DELETE("/sessions/:id/previews/:previewId", deps.Runtime.RevokePreview)
		v1.GET("/downloads/:downloadId/file", deps.Auth.DownloadRequired(deps.Runtime.DownloadOwner), deps.Runtime.DownloadContent)
		v1.HEAD("/downloads/:downloadId/file", deps.Auth.DownloadRequired(deps.Runtime.DownloadOwner), deps.Runtime.DownloadContent)
		authed.POST("/sessions/:id/downloads", deps.Runtime.CreateDownload)
		authed.GET("/sessions/:id/downloads/:downloadId", deps.Runtime.DownloadStatus)
		authed.DELETE("/sessions/:id/downloads/:downloadId", deps.Runtime.CancelDownload)
		authed.DELETE("/sessions/:id", deps.Runtime.DeleteSession)
		authed.POST("/sessions/:id/prompts", deps.Runtime.Prompt)
		authed.POST("/sessions/:id/cancel", deps.Runtime.Cancel)
		authed.POST("/sessions/:id/approvals/:callId", deps.Runtime.Approval)
		authed.POST("/sessions/:id/questions/:callId/answer", deps.Runtime.AnswerQuestion)
		authed.GET("/sessions/:id/files", deps.Runtime.Files)
		authed.GET("/sessions/:id/file", deps.Runtime.File)
		authed.POST("/sessions/:id/files", deps.Runtime.UploadFile)
		authed.POST("/sessions/:id/files/save-to-storage", deps.Runtime.SaveToStorage)
	} else {
		authed.DELETE("/sessions/:id", sessionH.DeleteSession)
	}
	authed.GET("/sessions/:id/events", sessionH.ListEvents)

	org := authed.Group("/orgs/:orgId", deps.Auth.ResolveOrg())
	if deps.Runtime != nil {
		deps.Runtime.SetFileSpaces(deps.DB)
		org.GET("/files", deps.Runtime.Files)
		org.GET("/file", deps.Runtime.File)
		org.POST("/files", deps.Runtime.UploadFile)
		org.POST("/file-directories", deps.Runtime.CreateDirectory)
		org.GET("/file-agents", deps.Runtime.FileAgents)
		org.POST("/file-previews", deps.Runtime.CreatePreview)
		org.DELETE("/file-previews/:previewId", deps.Runtime.RevokePreview)
		org.POST("/file-downloads", deps.Runtime.CreateDownload)
		org.GET("/file-downloads/:downloadId", deps.Runtime.DownloadStatus)
		org.DELETE("/file-downloads/:downloadId", deps.Runtime.CancelDownload)
	}
	if deps.Computers != nil {
		h := computer.NewHandler(deps.Computers)
		org.GET("/computers", h.List)
		org.POST("/computers", h.Create)
		org.GET("/agent-bindings", h.Bindings)
		org.PUT("/agent-bindings/:agentId", h.Bind)
		org.DELETE("/agent-bindings/:agentId", h.Unbind)
		org.GET("/volumes", h.Volumes)
		authed.GET("/computers/:id", h.Get)
		authed.GET("/computers/:id/env", h.Env)
		authed.PATCH("/computers/:id", h.Patch)
		authed.DELETE("/computers/:id", h.Action("delete"))
		authed.POST("/computers/:id/start", h.Action("start"))
		authed.POST("/computers/:id/pause", h.Action("pause"))
		authed.POST("/computers/:id/rebuild", h.Action("rebuild"))
		authed.POST("/computers/:id/recover", h.Recover)
		authed.GET("/computers/:id/runs", h.Runs)
		authed.GET("/operations/:id", h.Operation)
	}
	org.GET("/models", providerH.ListWorkspaceModels)
	org.GET("/agents", agentH.ListWorkspaceAgents)
	org.POST("/sessions", sessionH.CreateSession)
	org.GET("/sessions", sessionH.ListSessions)
	admin := org.Group("/admin", auth.RequireOrgAdmin())
	admin.GET("/members", tenantH.ListMembers)
	admin.POST("/members", tenantH.AddMember)
	admin.PATCH("/members/:userId", tenantH.UpdateMemberRole)
	admin.DELETE("/members/:userId", tenantH.RemoveMember)
	admin.GET("/agents", agentH.ListAgents)
	admin.POST("/agents", agentH.CreateAgent)
	admin.GET("/agents/:id", agentH.GetAgent)
	admin.PUT("/agents/:id", agentH.UpdateAgent)
	admin.PATCH("/agents/:id/env", agentH.UpdateEnv)
	admin.POST("/agents/:id/publish", agentH.PublishAgent)
	admin.POST("/agents/:id/archive", agentH.ArchiveAgent)
	admin.DELETE("/agents/:id", agentH.DeleteAgent)
	skillH := skill.NewHandler(skill.NewService(deps.DB, deps.Config.Storage.DataDir))
	admin.POST("/skills", skillH.UploadSkill)
	admin.PUT("/skills/:id/package", skillH.ReplacePackage)
	admin.GET("/skills", skillH.ListSkills)
	admin.GET("/skills/:id", skillH.GetSkill)
	admin.GET("/skills/:id/file", skillH.GetSkillFile)
	admin.DELETE("/skills/:id", skillH.DeleteSkill)
	admin.GET("/sandbox-settings", providerH.GetSandboxSettings)
	admin.GET("/search-providers", searchH.Options)
	usageH := usage.NewHandler(usage.NewService(deps.DB))
	admin.GET("/usage", usageH.OrgUsage)

	platform := authed.Group("/platform", deps.Auth.RequirePlatformAdmin())
	platform.GET("/orgs", tenantH.ListOrgs)
	platform.POST("/orgs", tenantH.CreateOrg)
	platform.GET("/orgs/:id", tenantH.GetOrg)
	platform.PATCH("/orgs/:id", tenantH.UpdateOrg)
	platform.GET("/users", tenantH.ListUsers)
	platform.POST("/users", tenantH.CreateUser)
	platform.PATCH("/users/:id", tenantH.UpdateUser)
	platform.POST("/users/:id/orgs", tenantH.AddUserOrg)
	platform.PATCH("/users/:id/orgs/:orgId", tenantH.UpdateUserOrgRole)
	platform.DELETE("/users/:id/orgs/:orgId", tenantH.RemoveUserOrg)
	platform.GET("/providers", providerH.ListProviders)
	platform.GET("/web-search/providers", searchH.List)
	platform.POST("/web-search/providers", searchH.Save)
	platform.PUT("/web-search/providers/:id", searchH.Save)
	platform.DELETE("/web-search/providers/:id", searchH.Delete)
	platform.POST("/web-search/providers/:id/check", searchH.Check)
	platform.GET("/web-search/config", searchH.Config)
	platform.PUT("/web-search/config", searchH.SetDefault)
	platform.PUT("/web-search/fetch-config", searchH.SetFetchDefault)
	platform.POST("/web-search/providers/:id/fetch-check", searchH.FetchCheck)
	platform.POST("/providers", providerH.CreateProvider)
	platform.GET("/providers/:id", providerH.GetProvider)
	platform.PUT("/providers/:id", providerH.UpdateProvider)
	platform.DELETE("/providers/:id", providerH.DeleteProvider)
	platform.POST("/providers/:id/check", providerH.CheckProvider)
	platform.GET("/models", providerH.ListModels)
	platform.POST("/models", providerH.CreateModel)
	platform.PUT("/models/:id", providerH.UpdateModel)
	platform.DELETE("/models/:id", providerH.DeleteModel)
	platform.GET("/settings", providerH.GetSettings)
	platform.PUT("/settings/:key", providerH.UpdateSetting)
	platform.GET("/usage", usageH.PlatformUsage)
	platform.GET("/audit", audit.NewHandler(deps.DB).List)

	if deps.WS != nil {
		r.GET("/api/ws", deps.WS.Handle)
	}

	r.NoRoute(spaHandler())
	return r
}
