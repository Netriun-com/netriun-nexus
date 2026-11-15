// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrationFilesRejectDuplicateVersions(t *testing.T) {
	for _, second := range []string{"014_service_queue.sql", "14_service_queue.sql"} {
		t.Run(second, func(t *testing.T) {
			_, err := readMigrations(fstest.MapFS{
				"migrations/014_audit_retention.sql": {},
				"migrations/" + second:               {},
			})
			if err == nil {
				t.Fatal("accepted duplicate migration version")
			}
			for _, detail := range []string{"duplicate migration version 14", "014_audit_retention.sql", second} {
				if !strings.Contains(err.Error(), detail) {
					t.Fatalf("error %q does not identify %q", err, detail)
				}
			}
		})
	}
}

func TestMigrationFilesDistinctVersions(t *testing.T) {
	migrations, err := readMigrations(fstest.MapFS{
		"migrations/015_audit_retention.sql": {},
		"migrations/014_service_queue.sql":   {},
		"migrations/README.md":               {},
		"migrations/archive.sql/old.txt":     {},
	})
	want := []migrationFile{
		{name: "014_service_queue.sql", version: 14},
		{name: "015_audit_retention.sql", version: 15},
	}
	if err != nil || !reflect.DeepEqual(migrations, want) {
		t.Fatalf("migration files: %+v, err=%v; want %+v", migrations, err, want)
	}
}

func TestEmbeddedMigrationVersionsAreUnique(t *testing.T) {
	if _, err := readMigrations(assets); err != nil {
		t.Fatal(err)
	}
}
