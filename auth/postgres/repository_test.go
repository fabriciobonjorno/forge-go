package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/auth"
	authpostgres "github.com/fabriciobonjorno/forge-go/auth/postgres"
	"github.com/fabriciobonjorno/forge-go/postgres/postgrestest"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

func TestRepositoryResolvesCurrentIdentityAndRevocation(t *testing.T) {
	db := postgrestest.NewMigrated(t, authpostgres.Migrations())
	repo, err := authpostgres.New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	userID, orgID, tenantID, membershipID, roleID := uuid.MustNew(), uuid.MustNew(), uuid.MustNew(), uuid.MustNew(), uuid.MustNew()
	if _, err := db.Exec(ctx, `
		INSERT INTO forge_users (id, email, email_normalized, password_hash)
		VALUES ($1, 'Alice@example.com', 'alice@example.com', 'test-only');
		INSERT INTO forge_organizations (id, name) VALUES ($2, 'Acme');
		INSERT INTO forge_tenants (id, organization_id, slug) VALUES ($3, $2, 'acme');
		INSERT INTO forge_memberships (id, user_id, tenant_id) VALUES ($4, $1, $3);
		INSERT INTO forge_roles (id, tenant_id, name) VALUES ($5, $3, 'reader');
		INSERT INTO forge_permissions (name) VALUES ('tasks:read');
		INSERT INTO forge_role_permissions (role_id, permission) VALUES ($5, 'tasks:read');
		INSERT INTO forge_membership_roles (membership_id, role_id, tenant_id) VALUES ($4, $5, $3);
	`, userID, orgID, tenantID, membershipID, roleID); err != nil {
		t.Fatal(err)
	}

	token, err := repo.CreateSession(ctx, membershipID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.ResolveSession(ctx, token.Digest())
	if err != nil || !found {
		t.Fatalf("ResolveSession() found=%v err=%v", found, err)
	}
	if session.Principal.SubjectID() != userID || session.Principal.Tenant().ID != tenantID || !session.Principal.Can(auth.Permission("tasks:read")) {
		t.Fatalf("unexpected principal: subject=%s tenant=%s permissions=%v", session.Principal.SubjectID(), session.Principal.Tenant().ID, session.Principal.Permissions())
	}

	if _, err := db.Exec(ctx, `DELETE FROM forge_role_permissions WHERE role_id = $1 AND permission = 'tasks:read'`, roleID); err != nil {
		t.Fatal(err)
	}
	session, found, err = repo.ResolveSession(ctx, token.Digest())
	if err != nil || !found || session.Principal.Can(auth.Permission("tasks:read")) {
		t.Fatalf("permission change not reflected: found=%v permissions=%v err=%v", found, session.Principal.Permissions(), err)
	}

	if err := repo.RevokeSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ResolveSession(ctx, token.Digest()); err != nil || found {
		t.Fatalf("revoked session found=%v err=%v", found, err)
	}
	if err := repo.RevokeSession(ctx, token); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryFailsClosedForInactiveIdentityAndCredentialVersion(t *testing.T) {
	db := postgrestest.NewMigrated(t, authpostgres.Migrations())
	repo, err := authpostgres.New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	userID, orgID, tenantID, membershipID := uuid.MustNew(), uuid.MustNew(), uuid.MustNew(), uuid.MustNew()
	if _, err := db.Exec(ctx, `
		INSERT INTO forge_users (id, email, email_normalized, password_hash)
		VALUES ($1, 'bob@example.com', 'bob@example.com', 'test-only');
		INSERT INTO forge_organizations (id, name) VALUES ($2, 'Example');
		INSERT INTO forge_tenants (id, organization_id, slug) VALUES ($3, $2, 'example');
		INSERT INTO forge_memberships (id, user_id, tenant_id) VALUES ($4, $1, $3);
	`, userID, orgID, tenantID, membershipID); err != nil {
		t.Fatal(err)
	}
	token, err := repo.CreateSession(ctx, membershipID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeUserSessions(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ResolveSession(ctx, token.Digest()); err != nil || found {
		t.Fatalf("stale credential version found=%v err=%v", found, err)
	}

	if _, err := db.Exec(ctx, `UPDATE forge_memberships SET status = 'suspended' WHERE id = $1`, membershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateSession(ctx, membershipID, time.Now().Add(time.Hour)); !errors.Is(err, authpostgres.ErrMembershipRequired) {
		t.Fatalf("inactive membership error=%v", err)
	}
}
