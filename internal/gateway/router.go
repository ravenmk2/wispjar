package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway/ws"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime"
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
	authed.POST("/auth/change-password", deps.Auth.ChangePassword)
	authed.GET("/auth/me", deps.Auth.Me)

	tenantH := tenant.NewHandler(tenant.NewService(deps.DB))
	providerH := provider.NewHandler(provider.NewService(deps.DB, deps.Config.MasterKey()))
	agentSvc := agent.NewService(deps.DB)
	sessionSvc := session.NewService(deps.DB, agentSvc)
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
		authed.DELETE("/sessions/:id", deps.Runtime.DeleteSession)
		authed.POST("/sessions/:id/prompts", deps.Runtime.Prompt)
		authed.POST("/sessions/:id/cancel", deps.Runtime.Cancel)
		authed.POST("/sessions/:id/approvals/:callId", deps.Runtime.Approval)
		authed.GET("/sessions/:id/sandbox/files", deps.Runtime.SandboxFiles)
		authed.GET("/sessions/:id/sandbox/file", deps.Runtime.SandboxFile)
	} else {
		authed.DELETE("/sessions/:id", sessionH.DeleteSession)
	}
	authed.GET("/sessions/:id/events", sessionH.ListEvents)

	org := authed.Group("/orgs/:orgId", deps.Auth.ResolveOrg())
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
	admin.POST("/agents/:id/publish", agentH.PublishAgent)
	admin.POST("/agents/:id/archive", agentH.ArchiveAgent)
	admin.DELETE("/agents/:id", agentH.DeleteAgent)
	skillH := skill.NewHandler(skill.NewService(deps.DB, deps.Config.Storage.DataDir))
	admin.POST("/skills", skillH.UploadSkill)
	admin.GET("/skills", skillH.ListSkills)
	admin.GET("/skills/:id", skillH.GetSkill)
	admin.GET("/skills/:id/file", skillH.GetSkillFile)
	admin.DELETE("/skills/:id", skillH.DeleteSkill)
	admin.GET("/sandbox-settings", providerH.GetSandboxSettings)
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
