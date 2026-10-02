package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/tenant"
)

type Deps struct {
	Config *config.Config
	DB     *gorm.DB
	Auth   *auth.Service
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

	org := authed.Group("/orgs/:orgId", deps.Auth.ResolveOrg())
	org.GET("/models", providerH.ListWorkspaceModels)
	admin := org.Group("/admin", auth.RequireOrgAdmin())
	admin.GET("/members", tenantH.ListMembers)
	admin.POST("/members", tenantH.AddMember)
	admin.PATCH("/members/:userId", tenantH.UpdateMemberRole)
	admin.DELETE("/members/:userId", tenantH.RemoveMember)

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

	r.NoRoute(spaHandler())
	return r
}
