package cli

import (
	"fmt"
	"strings"
)

// statement is one SQL statement and the Go expressions bound to its
// placeholders, in order. Building both together keeps them aligned.
type statement struct {
	SQL  string
	Args []string
}

// ArgList renders the bound arguments for a call.
func (s statement) ArgList() string { return strings.Join(s.Args, ", ") }

type sqlStatements struct {
	Columns   string // entity columns, in scan order
	Insert    statement
	Select    statement
	ListFirst statement
	ListAfter statement
	Update    statement
	Delete    statement
	Exists    statement
}

func buildStatements(v resourceView) sqlStatements {
	placeholder := func(n int) string {
		if v.Postgres {
			return fmt.Sprintf("$%d", n)
		}
		return "?"
	}
	// builder numbers placeholders as arguments are added.
	type builder struct {
		args []string
	}
	add := func(b *builder, arg string) string {
		b.args = append(b.args, arg)
		return placeholder(len(b.args))
	}
	tenantPredicate := func(b *builder) string {
		if !v.Tenant {
			return ""
		}
		return "tenant_id = " + add(b, "tenantID") + " AND "
	}

	columns := []string{"id"}
	for _, f := range v.Fields {
		columns = append(columns, f.Name)
	}
	columns = append(columns, "version", "created_at", "updated_at")
	list := strings.Join(columns, ", ")
	statements := sqlStatements{Columns: list}

	// INSERT binds tenant_id right after id.
	{
		var b builder
		names := []string{"id"}
		values := []string{add(&b, "entity.ID")}
		if v.Tenant {
			names = append(names, "tenant_id")
			values = append(values, add(&b, "tenantID"))
		}
		for _, f := range v.Fields {
			names = append(names, f.Name)
			values = append(values, add(&b, "entity."+f.Title))
		}
		for _, column := range []struct{ name, expr string }{
			{"version", "entity.Version"}, {"created_at", "entity.CreatedAt"}, {"updated_at", "entity.UpdatedAt"},
		} {
			names = append(names, column.name)
			values = append(values, add(&b, column.expr))
		}
		statements.Insert = statement{
			SQL:  fmt.Sprintf("INSERT INTO %s (%s)\nVALUES (%s)", v.Plural, strings.Join(names, ", "), strings.Join(values, ", ")),
			Args: b.args,
		}
	}
	{
		var b builder
		where := tenantPredicate(&b) + "id = " + add(&b, "id")
		statements.Select = statement{SQL: fmt.Sprintf("SELECT %s FROM %s WHERE %s", list, v.Plural, where), Args: b.args}
	}
	{
		var b builder
		where := ""
		if v.Tenant {
			where = " WHERE tenant_id = " + add(&b, "tenantID")
		}
		statements.ListFirst = statement{
			SQL:  fmt.Sprintf("SELECT %s FROM %s%s ORDER BY id DESC LIMIT %s", list, v.Plural, where, add(&b, "limit")),
			Args: b.args,
		}
	}
	{
		var b builder
		where := tenantPredicate(&b) + "id < " + add(&b, "after")
		statements.ListAfter = statement{
			SQL:  fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY id DESC LIMIT %s", list, v.Plural, where, add(&b, "limit")),
			Args: b.args,
		}
	}
	{
		var b builder
		assignments := make([]string, 0, len(v.Fields)+2)
		for _, f := range v.Fields {
			assignments = append(assignments, f.Name+" = "+add(&b, "entity."+f.Title))
		}
		assignments = append(assignments, "updated_at = "+add(&b, "entity.UpdatedAt"), "version = version + 1")
		where := tenantPredicate(&b) + "id = " + add(&b, "entity.ID") + " AND version = " + add(&b, "entity.Version")
		statements.Update = statement{
			SQL:  fmt.Sprintf("UPDATE %s\nSET %s\nWHERE %s", v.Plural, strings.Join(assignments, ", "), where),
			Args: b.args,
		}
	}
	{
		var b builder
		where := tenantPredicate(&b) + "id = " + add(&b, "id") + " AND version = " + add(&b, "version")
		statements.Delete = statement{SQL: fmt.Sprintf("DELETE FROM %s WHERE %s", v.Plural, where), Args: b.args}
	}
	{
		var b builder
		where := tenantPredicate(&b) + "id = " + add(&b, "id")
		statements.Exists = statement{SQL: fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s)", v.Plural, where), Args: b.args}
	}
	return statements
}

// column is one line of CREATE TABLE.
type column struct {
	name, definition string
}

// columnDefinition returns the column type and constraints for one field on
// the application's database. Strings are bounded at 255 characters; the
// domain enforces the same bound before the database sees a value.
func columnDefinition(database, table string, f resourceField) string {
	null := " NOT NULL"
	if f.Optional {
		null = ""
	}
	switch database {
	case "postgres":
		switch f.Type {
		case "string":
			return fmt.Sprintf("text%s CONSTRAINT %s_%s_length CHECK (char_length(%s) <= 255)", null, table, f.Name, f.Name)
		case "text":
			return "text" + null
		case "int":
			return "bigint" + null
		case "bool":
			return "boolean" + null
		case "time":
			return "timestamptz" + null
		default:
			return "uuid" + null
		}
	case "mysql":
		switch f.Type {
		case "string":
			return "VARCHAR(255)" + null
		case "text":
			return "TEXT" + null
		case "int":
			return "BIGINT" + null
		case "bool":
			return "BOOLEAN" + null
		case "time":
			return "DATETIME(6)" + null
		default:
			return "CHAR(36)" + null
		}
	default: // sqlite: STRICT tables allow only INT, INTEGER, REAL, TEXT, BLOB and ANY.
		switch f.Type {
		case "string":
			return fmt.Sprintf("TEXT%s CONSTRAINT %s_%s_length CHECK (length(%s) <= 255)", null, table, f.Name, f.Name)
		case "text", "time", "uuid":
			return "TEXT" + null
		case "int":
			return "INTEGER" + null
		default: // bool
			return fmt.Sprintf("INTEGER%s CONSTRAINT %s_%s_boolean CHECK (%s IN (0, 1))", null, table, f.Name, f.Name)
		}
	}
}

// renderMigration builds the up and down migrations. The table has the same
// shape on every database: a UUIDv7 key, the user's fields, an optimistic
// locking version and UTC timestamps.
func renderMigration(v resourceView) (up, down string) {
	table := v.Plural
	var columns []column
	switch {
	case v.Postgres:
		columns = append(columns, column{"id", "uuid PRIMARY KEY DEFAULT uuidv7()"})
	case v.MySQL:
		columns = append(columns, column{"id", "CHAR(36) NOT NULL PRIMARY KEY"})
	default:
		columns = append(columns, column{"id", "TEXT NOT NULL PRIMARY KEY"})
	}
	if v.Tenant {
		switch {
		case v.Postgres:
			columns = append(columns, column{"tenant_id", "uuid NOT NULL"})
		case v.MySQL:
			columns = append(columns, column{"tenant_id", "CHAR(36) NOT NULL"})
		default:
			columns = append(columns, column{"tenant_id", "TEXT NOT NULL"})
		}
	}
	for _, f := range v.Fields {
		columns = append(columns, column{f.Name, columnDefinition(v.Database, table, f.resourceField)})
	}
	switch {
	case v.Postgres:
		columns = append(columns,
			column{"version", fmt.Sprintf("integer NOT NULL DEFAULT 1 CONSTRAINT %s_version_positive CHECK (version >= 1)", table)},
			column{"created_at", "timestamptz NOT NULL"},
			column{"updated_at", "timestamptz NOT NULL"})
	case v.MySQL:
		columns = append(columns,
			column{"version", "INT NOT NULL DEFAULT 1"},
			column{"created_at", "DATETIME(6) NOT NULL"},
			column{"updated_at", "DATETIME(6) NOT NULL"})
	default:
		columns = append(columns,
			column{"version", fmt.Sprintf("INTEGER NOT NULL DEFAULT 1 CONSTRAINT %s_version_positive CHECK (version >= 1)", table)},
			column{"created_at", "TEXT NOT NULL"},
			column{"updated_at", "TEXT NOT NULL"})
	}

	width := 0
	for _, c := range columns {
		width = max(width, len(c.name))
	}
	lines := make([]string, 0, len(columns)+2)
	for _, c := range columns {
		lines = append(lines, fmt.Sprintf("    %-*s %s", width, c.name, c.definition))
	}
	if v.MySQL {
		lines = append(lines, fmt.Sprintf("    CONSTRAINT %s_version_positive CHECK (version >= 1)", table))
		if v.Tenant {
			lines = append(lines, fmt.Sprintf("    INDEX %s_tenant_id_idx (tenant_id, id)", table))
		}
	}

	var sql strings.Builder
	fmt.Fprintf(&sql, "-- Creates %s. Ids are UUIDv7 generated by the application", table)
	switch {
	case v.Postgres:
		sql.WriteString("; the uuidv7() default\n-- (PostgreSQL 18+) only covers rows inserted by hand.\n")
	case v.MySQL:
		sql.WriteString(" and stored as CHAR(36).\n")
	default:
		sql.WriteString(" and stored as TEXT in a STRICT table.\n")
	}
	fmt.Fprintf(&sql, "CREATE TABLE %s (\n%s\n)", table, strings.Join(lines, ",\n"))
	switch {
	case v.MySQL:
		sql.WriteString(" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	case v.SQLite:
		sql.WriteString(" STRICT")
	}
	sql.WriteString(";\n")
	if v.Tenant && !v.MySQL {
		fmt.Fprintf(&sql, "\nCREATE INDEX %s_tenant_id_idx ON %s (tenant_id, id);\n", table, table)
	}
	if v.Tenant && v.Postgres {
		predicate := "tenant_id = NULLIF(current_setting('forge.tenant_id', true), '')::uuid"
		fmt.Fprintf(&sql, "\n-- Defense in depth: even a query that forgets its tenant_id predicate sees only\n-- the tenant set by InTenantTx. FORCE applies the policy to the table owner too.\n")
		fmt.Fprintf(&sql, "ALTER TABLE %s ENABLE ROW LEVEL SECURITY;\nALTER TABLE %s FORCE ROW LEVEL SECURITY;\n\n", table, table)
		fmt.Fprintf(&sql, "CREATE POLICY %s_tenant ON %s\n    USING (%s)\n    WITH CHECK (%s);\n", table, table, predicate, predicate)
	}
	if v.Identity {
		fmt.Fprintf(&sql, "\n-- Permissions that guard the HTTP routes (the protect argument of Register%s).\n", v.PluralTitle)
		fmt.Fprintf(&sql, "INSERT INTO forge_permissions (name) VALUES ('%s:read'), ('%s:write')\nON CONFLICT (name) DO NOTHING;\n", table, table)
	}
	up = sql.String()

	var rollback strings.Builder
	if v.Identity {
		fmt.Fprintf(&rollback, "DELETE FROM forge_role_permissions WHERE permission IN ('%s:read', '%s:write');\n", table, table)
		fmt.Fprintf(&rollback, "DELETE FROM forge_permissions WHERE name IN ('%s:read', '%s:write');\n", table, table)
	}
	fmt.Fprintf(&rollback, "DROP TABLE %s;\n", table)
	down = rollback.String()
	return up, down
}
