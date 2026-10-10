//go:build localjournal

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
)

// Only the private owned fixture is accepted; no production baseline migrations or grants run.
func journalMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	var meta struct {
		Socket  string `json:"socket_dir"`
		Data    string `json:"data_dir"`
		Started bool   `json:"started"`
		TCP     bool   `json:"listen_tcp"`
	}
	b, e := os.ReadFile(os.Getenv("SUB2_JOURNAL_ENV"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &meta); e != nil {
		t.Fatal(e)
	}
	if !meta.Started || meta.TCP || !strings.HasPrefix(meta.Socket, "/tmp/sub2usage-pg18-") {
		t.Fatal("private fixture required")
	}
	dsn := fmt.Sprintf("host='%s' port=54472 user=synthetic_owner dbname=postgres sslmode=disable", meta.Socket)
	admin, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	var data, user, listen string
	if e = admin.QueryRow("SELECT current_setting('data_directory'),current_user,current_setting('listen_addresses')").Scan(&data, &user, &listen); e != nil {
		t.Fatal(e)
	}
	if data != meta.Data || user != "synthetic_owner" || listen != "" {
		t.Fatal("fixture identity mismatch")
	}
	schema := fmt.Sprintf("synthetic_journal_registration_%d", time.Now().UnixNano())
	if _, e = admin.Exec("CREATE SCHEMA " + schema); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("postgres", dsn+" options='-c search_path="+schema+"'")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(3)
	t.Cleanup(func() {
		_ = db.Close()
		a, e := sql.Open("postgres", dsn)
		if e != nil {
			t.Error(e)
			return
		}
		defer a.Close()
		if _, e = a.Exec("DROP SCHEMA " + schema + " CASCADE"); e != nil {
			t.Error(e)
		}
	})
	return db
}

func TestJournalMigrationRegistration(t *testing.T) {
	const name = "241_usage_recovery_journal_v1.sql"
	body, e := migrations.FS.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	selected := func(b []byte) fstest.MapFS { return fstest.MapFS{name: &fstest.MapFile{Data: b}} }
	t.Run("apply_repeat_checksum", func(t *testing.T) {
		db := journalMigrationDB(t)
		ctx := context.Background()
		if e := applyMigrationsFS(ctx, db, selected(body)); e != nil {
			t.Fatal(e)
		}
		version, signature, e := journalCatalogIdentity(ctx, db)
		if e != nil {
			t.Fatal(e)
		}
		if version != UsageJournalSchemaVersion || signature != UsageJournalSchemaSignature {
			t.Fatalf("catalog mismatch: %s %s", version, signature)
		}
		if e := applyMigrationsFS(ctx, db, selected(body)); e != nil {
			t.Fatal(e)
		}
		var n int
		if e := db.QueryRow("SELECT count(*) FROM schema_migrations WHERE filename=$1", name).Scan(&n); e != nil || n != 1 {
			t.Fatalf("ledger count %d: %v", n, e)
		}
		altered := append(append([]byte{}, body...), []byte("\n-- changed checksum\n")...)
		if e := applyMigrationsFS(ctx, db, selected(altered)); e == nil || !strings.Contains(e.Error(), "checksum") {
			t.Fatalf("checksum change accepted: %v", e)
		}
	})
	t.Run("ddl_failure_rolls_back", func(t *testing.T) {
		db := journalMigrationDB(t)
		broken := append(append([]byte{}, body...), []byte("\nSELECT 1/0;\n")...)
		if e := applyMigrationsFS(context.Background(), db, selected(broken)); e == nil {
			t.Fatal("injected error accepted")
		}
		var absent bool
		var n int
		if e := db.QueryRow("SELECT to_regclass('usage_recovery_journal_v1') IS NULL").Scan(&absent); e != nil || !absent {
			t.Fatalf("DDL leaked: %v %v", absent, e)
		}
		if e := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); e != nil || n != 0 {
			t.Fatalf("ledger leaked: %d %v", n, e)
		}
		if e := applyMigrationsFS(context.Background(), db, selected(body)); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("existing_object_collision", func(t *testing.T) {
		db := journalMigrationDB(t)
		if _, e := db.Exec("CREATE TABLE usage_recovery_journal_v1 (sentinel text); INSERT INTO usage_recovery_journal_v1 VALUES ('synthetic')"); e != nil {
			t.Fatal(e)
		}
		if e := applyMigrationsFS(context.Background(), db, selected(body)); e == nil {
			t.Fatal("collision silently accepted")
		}
		var sentinel string
		var n int
		if e := db.QueryRow("SELECT sentinel FROM usage_recovery_journal_v1").Scan(&sentinel); e != nil || sentinel != "synthetic" {
			t.Fatalf("existing object changed: %v", e)
		}
		if e := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); e != nil || n != 0 {
			t.Fatalf("collision ledger changed: %d %v", n, e)
		}
	})
}
