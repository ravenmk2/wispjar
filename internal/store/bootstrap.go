package store

import (
	"crypto/rand"
	"errors"
	"log"
	"math/big"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/id"
)

func BootstrapAdmin(db *gorm.DB, password string) error {
	var count int64
	if err := db.Model(&OrgMember{}).
		Where("org_id = ? AND role = ?", id.PlatformOrgID, "admin").
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var user User
		err := tx.Where("username = ?", "admin").First(&user).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			generated := false
			if password == "" {
				password, err = randomPassword(16)
				if err != nil {
					return err
				}
				generated = true
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
			if err != nil {
				return err
			}
			user = User{
				ID:                 id.New(),
				Username:           "admin",
				PasswordHash:       string(hash),
				DisplayName:        "系统管理员",
				Status:             "active",
				MustChangePassword: true,
				CreatedAt:          time.Now(),
				UpdatedAt:          time.Now(),
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			if generated {
				log.Printf("bootstrap: created admin user, initial password: %s", password)
			} else {
				log.Printf("bootstrap: created admin user (password from WISPJAR_ADMIN_PASSWORD)")
			}
		case err != nil:
			return err
		}

		member := OrgMember{
			ID:        id.New(),
			OrgID:     id.PlatformOrgID,
			UserID:    user.ID,
			Role:      "admin",
			CreatedAt: time.Now(),
		}
		return tx.Create(&member).Error
	})
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
		j := int(x.Int64())
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf), nil
}
