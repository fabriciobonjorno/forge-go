package mysql

import (
	"strings"
	"testing"
	"time"
)

func TestDetectFlavor(t *testing.T) {
	t.Parallel()
	for version, want := range map[string]flavor{
		"8.4.5":                  flavorMySQL,
		"8.0.36-0ubuntu0.22.04":  flavorMySQL,
		"11.8.2-MariaDB-ubu2404": flavorMariaDB,
		"10.11.6-MariaDB-log":    flavorMariaDB,
		"5.5.5-10.6.12-mariadb":  flavorMariaDB,
	} {
		if got := detectFlavor(version); got != want {
			t.Errorf("%s: flavor=%d want %d", version, got, want)
		}
	}
}

func TestSessionSetup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		flavor  flavor
		timeout time.Duration
		want    string
	}{
		{flavorMySQL, 30 * time.Second, "max_execution_time = 30000"},
		{flavorMySQL, 0, "max_execution_time = 0"},
		{flavorMariaDB, 30 * time.Second, "max_statement_time = 30.000"},
		{flavorMariaDB, 1500 * time.Millisecond, "max_statement_time = 1.500"},
		{flavorMariaDB, time.Millisecond, "max_statement_time = 0.001"},
		{flavorMariaDB, 0, "max_statement_time = 0.000"},
	}
	for _, test := range tests {
		setup := sessionSetup(test.flavor, test.timeout)
		if !strings.HasSuffix(setup, ", "+test.want) {
			t.Errorf("%d/%v: %s", test.flavor, test.timeout, setup)
		}
		for _, required := range []string{"SET NAMES utf8mb4", "time_zone = '+00:00'", "STRICT_ALL_TABLES"} {
			if !strings.Contains(setup, required) {
				t.Errorf("session setup lacks %s: %s", required, setup)
			}
		}
	}
}
