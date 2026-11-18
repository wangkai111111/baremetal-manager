package platform

import (
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"testing"
)

// Called inside each integration test transaction; never creates persistent accounts.
func seedIntegrationAdmin(t *testing.T, db *gorm.DB) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-only-password-2026"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var uid int64
	if err = db.Raw(`INSERT INTO users(username,password_hash,name,is_platform_admin) VALUES ('integration-admin',?,'Integration Admin',true)
 ON CONFLICT (lower(username)) WHERE deleted_at IS NULL DO UPDATE SET password_hash=EXCLUDED.password_hash,is_platform_admin=true,status='active' RETURNING id`, string(hash)).Scan(&uid).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id) SELECT id,? FROM tenants WHERE deleted_at IS NULL
 ON CONFLICT (tenant_id,user_id) DO UPDATE SET status='active',deleted_at=NULL`, uid).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`INSERT INTO memberships(tenant_id,project_id,user_id,role_id)
 SELECT p.tenant_id,p.id,?,r.id FROM projects p JOIN roles r ON r.tenant_id=p.tenant_id AND r.code='manager' AND r.deleted_at IS NULL
 WHERE p.deleted_at IS NULL ON CONFLICT (tenant_id,project_id,user_id,role_id) WHERE deleted_at IS NULL DO NOTHING`, uid).Error; err != nil {
		t.Fatal(err)
	}
}
