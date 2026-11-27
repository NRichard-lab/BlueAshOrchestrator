package sqlite

import (
	"strings"
	"testing"
)

const localHarnessesMigrationVersion int64 = 20261127034821235

// localHarnessesPreviousVersion is the newest migration before the local
// harnesses'. Rolling back to it undoes only that step.
const localHarnessesPreviousVersion int64 = 20261115072651599

// TestMigrationAllowsBothLocalHarnessesAndReverses covers the two local
// harnesses the same way the MiniMax Code migration is covered: each must be
// insertable after the migration, an unknown harness must still be rejected,
// and the down migration must restore the exact prior constraint.
//
// 'local' is here as well as 'local-models' because it shipped without a
// migration: until this one, a session on it failed the CHECK.
func TestMigrationAllowsBothLocalHarnessesAndReverses(t *testing.T) {
	db := openMigratedDatabaseCopy(t, localHarnessesPreviousVersion)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('lm-project', '/tmp/lm-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'lm-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-omp", 1, "omp")
	for i, harness := range []string{"local", "local-models"} {
		if _, err := db.Exec(insert, "before-"+harness, 10+i, harness); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("%s insertion before the migration = %v; want CHECK failure", harness, err)
		}
	}

	upTo(t, db, localHarnessesMigrationVersion)
	assertAppliedMigrations(t, db, localHarnessesMigrationVersion)
	for i, harness := range []string{"local", "local-models"} {
		if _, err := db.Exec(insert, "after-"+harness, 20+i, harness); err != nil {
			t.Fatalf("insert %s session after migration: %v", harness, err)
		}
	}
	if _, err := db.Exec(insert, "unknown", 30, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	var widened string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&widened); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(widened, "'minimax-code', 'local', 'local-models', 'fake'))") {
		t.Fatalf("local harnesses are not the newest entries before 'fake': %s", widened)
	}

	// Clear the new harness values before downgrading to the older contract.
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness IN ('local', 'local-models')`)
	downTo(t, db, localHarnessesPreviousVersion)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore the original schema: %v\nbefore: %s\nafter:  %s", err, before, after)
	}
	for i, harness := range []string{"local", "local-models"} {
		if _, err := db.Exec(insert, "down-"+harness, 40+i, harness); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("%s insertion after downgrade = %v; want CHECK failure", harness, err)
		}
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}

// A database whose migration receipt says the step ran but whose constraint
// lacks a local harness is repaired on open, each harness independently, and
// the result is in fresh-database order.
func TestHarnessRepairAddsAMissingLocalHarness(t *testing.T) {
	for _, missing := range []string{"local", "local-models"} {
		t.Run(missing, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, localHarnessesMigrationVersion)
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, ?, '') WHERE type = 'table' AND name = 'sessions'`, "'"+missing+"', ")
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			var broken string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&broken); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(broken, "'"+missing+"'") {
				t.Fatalf("test setup did not remove %s: %s", missing, broken)
			}
			if err := reconcileHarnessConstraint(db); err != nil {
				t.Fatal(err)
			}
			var repaired string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&repaired); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(repaired, "'minimax-code', 'local', 'local-models', 'fake'))") {
				t.Fatalf("local harnesses are not in fresh-database order after repair: %s", repaired)
			}
		})
	}
}
