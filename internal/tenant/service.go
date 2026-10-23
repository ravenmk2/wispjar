package tenant

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

func validRole(role string) bool {
	return role == RoleAdmin || role == RoleMember
}

func validStatus(status string) bool {
	return status == StatusActive || status == StatusDisabled
}

func removingLastAdmin(role string, adminCount int64) bool {
	return role == RoleAdmin && adminCount <= 1
}

type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

func errInvalid(msg string, details any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: respond.CodeInvalid, Message: msg, Details: details}
}

func errNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: respond.CodeNotFound, Message: msg}
}

func errConflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: respond.CodeConflict, Message: msg}
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

type OrgSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	IsPlatform  bool      `json:"is_platform"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	MemberCount int       `json:"member_count"`
	AgentCount  int       `json:"agent_count"`
}

type MemberEntry struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type memberRow struct {
	ID          string
	Username    string
	DisplayName string
	Status      string
	Role        string
	CreatedAt   time.Time
}

func toMemberEntries(rows []memberRow) []MemberEntry {
	entries := make([]MemberEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, MemberEntry{
			UserID:      r.ID,
			Username:    r.Username,
			DisplayName: r.DisplayName,
			Status:      r.Status,
			Role:        r.Role,
			CreatedAt:   r.CreatedAt,
		})
	}
	return entries
}

func (s *Service) memberEntryOf(db *gorm.DB, orgID, userID string) (*MemberEntry, error) {
	rows := []memberRow{}
	err := db.Model(&store.OrgMember{}).
		Select("user.id, user.username, user.display_name, user.status, org_member.role, org_member.created_at").
		Joins("JOIN user ON user.id = org_member.user_id").
		Where("org_member.org_id = ? AND org_member.user_id = ?", orgID, userID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound("该用户不是组织成员")
	}
	entries := toMemberEntries(rows)
	return &entries[0], nil
}

func (s *Service) membersOf(orgID string) ([]MemberEntry, error) {
	rows := []memberRow{}
	err := s.db.Model(&store.OrgMember{}).
		Select("user.id, user.username, user.display_name, user.status, org_member.role, org_member.created_at").
		Joins("JOIN user ON user.id = org_member.user_id").
		Where("org_member.org_id = ?", orgID).
		Order("org_member.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return toMemberEntries(rows), nil
}

func (s *Service) adminCount(tx *gorm.DB, orgID string) (int64, error) {
	var count int64
	err := tx.Model(&store.OrgMember{}).
		Where("org_id = ? AND role = ?", orgID, RoleAdmin).
		Count(&count).Error
	return count, err
}

func (s *Service) ListOrgs(cursor string, limit int) ([]OrgSummary, string, error) {
	q := s.db.Model(&store.Organization{}).
		Select("organization.*, COUNT(DISTINCT org_member.id) AS member_count, COUNT(DISTINCT agent.id) AS agent_count").
		Joins("LEFT JOIN org_member ON org_member.org_id = organization.id").
		Joins("LEFT JOIN agent ON agent.org_id = organization.id").
		Group("organization.id").
		Order("organization.id DESC").
		Limit(limit + 1)
	if cursor != "" {
		q = q.Where("organization.id < ?", cursor)
	}
	rows := []OrgSummary{}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = rows[limit-1].ID
		rows = rows[:limit]
	}
	return rows, next, nil
}

func (s *Service) CreateOrg(actorID, name string) (*store.Organization, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return nil, errInvalid("组织名称必填且不超过 128 字符", nil)
	}
	org := store.Organization{
		ID:     id.New(),
		Name:   name,
		Status: StatusActive,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&org).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.OrgCreate,
			Target:  org.ID,
			Detail:  map[string]any{"name": org.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return &org, nil
}

type OrgDetail struct {
	store.Organization
	MemberCount int           `json:"member_count"`
	AgentCount  int           `json:"agent_count"`
	Members     []MemberEntry `json:"members"`
}

func (s *Service) GetOrg(orgID string) (*OrgDetail, error) {
	var org store.Organization
	if err := s.db.First(&org, "id = ?", orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("组织不存在")
		}
		return nil, err
	}
	var memberCount, agentCount int64
	if err := s.db.Model(&store.OrgMember{}).Where("org_id = ?", orgID).Count(&memberCount).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&store.Agent{}).Where("org_id = ?", orgID).Count(&agentCount).Error; err != nil {
		return nil, err
	}
	members, err := s.membersOf(orgID)
	if err != nil {
		return nil, err
	}
	return &OrgDetail{
		Organization: org,
		MemberCount:  int(memberCount),
		AgentCount:   int(agentCount),
		Members:      members,
	}, nil
}

type UpdateOrgInput struct {
	Name   *string
	Status *string
}

func (s *Service) UpdateOrg(actorID, orgID string, in UpdateOrgInput) (*store.Organization, error) {
	var org store.Organization
	if err := s.db.First(&org, "id = ?", orgID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("组织不存在")
		}
		return nil, err
	}
	updates := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 128 {
			return nil, errInvalid("组织名称必填且不超过 128 字符", nil)
		}
		updates["name"] = name
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return nil, errInvalid("status 必须是 active 或 disabled", map[string]any{"status": *in.Status})
		}
		if *in.Status == StatusDisabled && org.IsPlatform {
			return nil, errConflict("系统管理组织不可停用")
		}
		updates["status"] = *in.Status
	}
	if len(updates) == 0 {
		return &org, nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&org).Updates(updates).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.OrgUpdate,
			Target:  org.ID,
			Detail:  updates,
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.db.First(&org, "id = ?", orgID).Error; err != nil {
		return nil, err
	}
	return &org, nil
}

type UserOrgEntry struct {
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

type UserView struct {
	store.User
	Orgs []UserOrgEntry `json:"orgs"`
}

func (s *Service) orgsOfUsers(userIDs []string) (map[string][]UserOrgEntry, error) {
	result := make(map[string][]UserOrgEntry, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows := []struct {
		UserID  string
		OrgID   string
		OrgName string `gorm:"column:org_name"`
		Role    string
	}{}
	err := s.db.Model(&store.OrgMember{}).
		Select("org_member.user_id, organization.id AS org_id, organization.name AS org_name, org_member.role").
		Joins("JOIN organization ON organization.id = org_member.org_id").
		Where("org_member.user_id IN ?", userIDs).
		Order("organization.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.UserID] = append(result[r.UserID], UserOrgEntry{
			OrgID:   r.OrgID,
			OrgName: r.OrgName,
			Role:    r.Role,
		})
	}
	return result, nil
}

func toUserViews(users []store.User, orgs map[string][]UserOrgEntry) []UserView {
	views := make([]UserView, 0, len(users))
	for _, u := range users {
		o := orgs[u.ID]
		if o == nil {
			o = []UserOrgEntry{}
		}
		views = append(views, UserView{User: u, Orgs: o})
	}
	return views
}

func (s *Service) userView(userID string) (*UserView, error) {
	var user store.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("用户不存在")
		}
		return nil, err
	}
	orgs, err := s.orgsOfUsers([]string{user.ID})
	if err != nil {
		return nil, err
	}
	views := toUserViews([]store.User{user}, orgs)
	return &views[0], nil
}

func (s *Service) ListUsers(q, cursor string, limit int) ([]UserView, string, error) {
	query := s.db.Model(&store.User{}).Order("id DESC").Limit(limit + 1)
	if cursor != "" {
		query = query.Where("id < ?", cursor)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + q + "%"
		query = query.Where("username LIKE ? OR display_name LIKE ?", like, like)
	}
	users := []store.User{}
	if err := query.Find(&users).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(users) > limit {
		next = users[limit-1].ID
		users = users[:limit]
	}
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	orgs, err := s.orgsOfUsers(ids)
	if err != nil {
		return nil, "", err
	}
	return toUserViews(users, orgs), next, nil
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	OrgIDs      []string
	Role        string
}

func randomPassword(n int) (string, error) {
	const letters = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ"
	const digits = "23456789"
	buf := make([]byte, 0, n)
	pick := func(charset string, count int) error {
		for i := 0; i < count; i++ {
			x, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				return err
			}
			buf = append(buf, charset[x.Int64()])
		}
		return nil
	}
	if err := pick(letters, n-4); err != nil {
		return "", err
	}
	if err := pick(digits, 4); err != nil {
		return "", err
	}
	for i := len(buf) - 1; i > 0; i-- {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		buf[i], buf[x.Int64()] = buf[x.Int64()], buf[i]
	}
	return string(buf), nil
}

func (s *Service) CreateUser(actorID string, in CreateUserInput) (*UserView, string, error) {
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		return nil, "", errInvalid("用户名必填且不超过 64 字符", nil)
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		displayName = username
	}
	role := in.Role
	if role == "" {
		role = RoleMember
	}
	if !validRole(role) {
		return nil, "", errInvalid("role 必须是 admin 或 member", map[string]any{"role": in.Role})
	}
	password := in.Password
	if password == "" {
		var err error
		password, err = randomPassword(12)
		if err != nil {
			return nil, "", err
		}
	} else if !auth.ValidateNewPassword(password) {
		return nil, "", errInvalid("密码至少 8 位且包含字母和数字", map[string]any{"password": "至少 8 位，包含字母和数字"})
	}
	var count int64
	if err := s.db.Model(&store.User{}).Where("username = ?", username).Count(&count).Error; err != nil {
		return nil, "", err
	}
	if count > 0 {
		return nil, "", errConflict("用户名已存在")
	}
	if len(in.OrgIDs) > 0 {
		var orgCount int64
		if err := s.db.Model(&store.Organization{}).Where("id IN ?", in.OrgIDs).Count(&orgCount).Error; err != nil {
			return nil, "", err
		}
		if int(orgCount) != len(in.OrgIDs) {
			return nil, "", errNotFound("部分组织不存在")
		}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, "", err
	}
	user := store.User{
		ID:                 id.New(),
		Username:           username,
		PasswordHash:       hash,
		DisplayName:        displayName,
		Status:             StatusActive,
		MustChangePassword: true,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		for _, orgID := range in.OrgIDs {
			member := store.OrgMember{
				ID:     id.New(),
				OrgID:  orgID,
				UserID: user.ID,
				Role:   role,
			}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
			if err := audit.Log(tx, audit.Entry{
				OrgID:   orgID,
				ActorID: actorID,
				Action:  audit.MemberAdd,
				Target:  user.ID,
				Detail:  map[string]any{"username": user.Username, "role": role},
			}); err != nil {
				return err
			}
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.UserCreate,
			Target:  user.ID,
			Detail:  map[string]any{"username": user.Username, "org_ids": in.OrgIDs, "role": role},
		})
	})
	if err != nil {
		return nil, "", err
	}
	view, err := s.userView(user.ID)
	if err != nil {
		return nil, "", err
	}
	return view, password, nil
}

type UpdateUserInput struct {
	DisplayName   *string
	Status        *string
	ResetPassword bool
}

func (s *Service) UpdateUser(actorID, userID string, in UpdateUserInput) (*UserView, string, error) {
	var user store.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errNotFound("用户不存在")
		}
		return nil, "", err
	}
	updates := map[string]any{}
	if in.DisplayName != nil {
		updates["display_name"] = strings.TrimSpace(*in.DisplayName)
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return nil, "", errInvalid("status 必须是 active 或 disabled", map[string]any{"status": *in.Status})
		}
		updates["status"] = *in.Status
	}
	newPassword := ""
	if in.ResetPassword {
		var err error
		newPassword, err = randomPassword(12)
		if err != nil {
			return nil, "", err
		}
		hash, err := auth.HashPassword(newPassword)
		if err != nil {
			return nil, "", err
		}
		updates["password_hash"] = hash
		updates["must_change_password"] = true
	}
	if len(updates) == 0 {
		view, err := s.userView(user.ID)
		if err != nil {
			return nil, "", err
		}
		return view, "", nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(updates).Error; err != nil {
			return err
		}
		if in.ResetPassword {
			if err := audit.Log(tx, audit.Entry{
				ActorID: actorID,
				Action:  audit.UserResetPassword,
				Target:  user.ID,
				Detail:  map[string]any{"username": user.Username},
			}); err != nil {
				return err
			}
		}
		if in.DisplayName != nil || in.Status != nil {
			fields := map[string]any{}
			if in.DisplayName != nil {
				fields["display_name"] = *in.DisplayName
			}
			if in.Status != nil {
				fields["status"] = *in.Status
			}
			if err := audit.Log(tx, audit.Entry{
				ActorID: actorID,
				Action:  audit.UserUpdate,
				Target:  user.ID,
				Detail:  fields,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	view, err := s.userView(user.ID)
	if err != nil {
		return nil, "", err
	}
	return view, newPassword, nil
}

func (s *Service) addMember(tx *gorm.DB, actorID, orgID string, user store.User, role string) (*store.OrgMember, error) {
	var count int64
	if err := tx.Model(&store.OrgMember{}).
		Where("org_id = ? AND user_id = ?", orgID, user.ID).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errConflict("用户已是该组织成员")
	}
	member := store.OrgMember{
		ID:     id.New(),
		OrgID:  orgID,
		UserID: user.ID,
		Role:   role,
	}
	if err := tx.Create(&member).Error; err != nil {
		return nil, err
	}
	if err := audit.Log(tx, audit.Entry{
		OrgID:   orgID,
		ActorID: actorID,
		Action:  audit.MemberAdd,
		Target:  user.ID,
		Detail:  map[string]any{"username": user.Username, "role": role},
	}); err != nil {
		return nil, err
	}
	return &member, nil
}

func (s *Service) AddUserToOrg(actorID, userID, orgID, role string) (*MemberEntry, error) {
	if !validRole(role) {
		return nil, errInvalid("role 必须是 admin 或 member", map[string]any{"role": role})
	}
	var user store.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("用户不存在")
		}
		return nil, err
	}
	var orgCount int64
	if err := s.db.Model(&store.Organization{}).Where("id = ?", orgID).Count(&orgCount).Error; err != nil {
		return nil, err
	}
	if orgCount == 0 {
		return nil, errNotFound("组织不存在")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		_, err := s.addMember(tx, actorID, orgID, user, role)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.memberEntryOf(s.db, orgID, userID)
}

func (s *Service) AddMemberByUsername(actorID, orgID, username, role string) (*MemberEntry, error) {
	if !validRole(role) {
		return nil, errInvalid("role 必须是 admin 或 member", map[string]any{"role": role})
	}
	var user store.User
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("用户不存在")
		}
		return nil, err
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		_, err := s.addMember(tx, actorID, orgID, user, role)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.memberEntryOf(s.db, orgID, user.ID)
}

func (s *Service) UpdateMemberRole(actorID, orgID, userID, role string) (*MemberEntry, error) {
	if !validRole(role) {
		return nil, errInvalid("role 必须是 admin 或 member", map[string]any{"role": role})
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var m store.OrgMember
		if err := tx.Where("org_id = ? AND user_id = ?", orgID, userID).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errNotFound("该用户不是组织成员")
			}
			return err
		}
		if m.Role == role {
			return nil
		}
		if m.Role == RoleAdmin {
			count, err := s.adminCount(tx, orgID)
			if err != nil {
				return err
			}
			if removingLastAdmin(m.Role, count) {
				return errConflict("不得移除本组织最后一名管理员")
			}
		}
		if err := tx.Model(&m).Update("role", role).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.MemberUpdateRole,
			Target:  userID,
			Detail:  map[string]any{"from": m.Role, "to": role},
		})
	})
	if err != nil {
		return nil, err
	}
	return s.memberEntryOf(s.db, orgID, userID)
}

func (s *Service) RemoveMember(actorID, orgID, userID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var m store.OrgMember
		if err := tx.Where("org_id = ? AND user_id = ?", orgID, userID).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errNotFound("该用户不是组织成员")
			}
			return err
		}
		if m.Role == RoleAdmin {
			count, err := s.adminCount(tx, orgID)
			if err != nil {
				return err
			}
			if removingLastAdmin(m.Role, count) {
				return errConflict("不得移除本组织最后一名管理员")
			}
		}
		if err := tx.Delete(&m).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.MemberRemove,
			Target:  userID,
			Detail:  map[string]any{"role": m.Role},
		})
	})
}
