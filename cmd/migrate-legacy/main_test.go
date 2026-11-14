// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/app"
	"golang.org/x/crypto/bcrypt"
)

func TestMigration(t *testing.T) {
	dsn := os.Getenv("TEST_MIGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MIGRATION_DATABASE_URL to an isolated ccmp_test migration database")
	}
	if !strings.Contains(dsn, "ccmp_test") {
		t.Fatal("unsafe target")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 required")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = app.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "TRUNCATE workspaces,users,access_groups,cloud_accounts,instances,audit_log RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	// A fresh schema has no workspace until application bootstrap creates one.
	var workspaceID int64
	if err = db.QueryRow(ctx, "INSERT INTO workspaces(name,slug) VALUES('Migration test','migration-test') RETURNING id").Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("legacy-password"), bcrypt.MinCost)
	source := filepath.Join(t.TempDir(), "legacy.db")
	sql := `CREATE TABLE users(id,username,password_hash,role); CREATE TABLE access_groups(id,name,description,security_policy); CREATE TABLE user_groups(user_id,group_id,role); CREATE TABLE aws_accounts(id,account_name,owner,group_id,access_key_id,secret_access_key); CREATE TABLE ec2_instances(id,aws_account_id,instance_id,region,name,state,instance_type,public_ip,private_ip,launch_time,vpc_id,subnet_id,iam_instance_profile,tags,security_groups); CREATE TABLE account_activity_log(id,created_at,user_id,aws_account_id,group_id,action,details); CREATE TABLE logs(id,timestamp,action,details,user_id); CREATE TABLE app_settings(key,value);
 INSERT INTO users VALUES(7,'legacyadmin','` + strings.Replace(string(hash), "$2a$", "$2y$", 1) + `','admin');
 INSERT INTO access_groups VALUES(4,'Legacy group','Imported','{"view_dashboard":true}'); INSERT INTO user_groups VALUES(7,4,'group_admin');
 INSERT INTO aws_accounts VALUES(3,'Legacy AWS','Owner',4,'testkey','testsecret');
 INSERT INTO ec2_instances VALUES(8,3,'i-test','us-east-1','Test','running','t3.micro','','',NULL,'vpc-test','subnet-test','','{}','[]');
 INSERT INTO logs VALUES(1,'2026-01-01 00:00:00','legacy.action','details',7);
 INSERT INTO app_settings VALUES('collector_interval_minutes','60');`
	cmd := exec.Command("sqlite3", source)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	if err = run(source, false); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("dry run persisted users")
	}
	var sequence int64
	if err = db.QueryRow(ctx, "SELECT last_value FROM users_id_seq").Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if sequence != 1 {
		t.Fatal("dry run mutated sequence")
	}
	if err = run(source, true); err != nil {
		t.Fatal(err)
	}
	var role, encrypted, password string
	if err = db.QueryRow(ctx, "SELECT role FROM user_groups").Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "manager" {
		t.Fatal("legacy role not mapped")
	}
	if err = db.QueryRow(ctx, "SELECT credentials FROM cloud_accounts").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "testsecret") {
		t.Fatal("plaintext secret imported")
	}
	if err = db.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=7").Scan(&password); err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(password), []byte("legacy-password")); err != nil {
		t.Fatal("legacy PHP bcrypt login failed")
	}
	var next int64
	if err = db.QueryRow(ctx, "INSERT INTO users(workspace_id,username,password_hash,role) VALUES($1,'next','unused','user') RETURNING id", workspaceID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 8 {
		t.Fatalf("sequence wrong: %d", next)
	}
	if err = run(source, true); err == nil {
		t.Fatal("populated target accepted")
	}
}
