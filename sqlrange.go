// Package sqlrange integrates database/sql with Go 1.22 range functions.
package sqlrange

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sync/atomic"
)

// ExecOption is a functional option type to configure the Exec and ExecContext
// functions.
type ExecOption[Row any] func(*execOptions[Row])

// ExecArgsFields constructs an option that specifies the fields to include in
// the query arguments from a list of column names.
//
// This option is useful when the query only needs a subset of the fields from
// the row type, or when the query arguments are in a different order than the
// struct fields.
func ExecArgsFields[Row any](columnNames ...string) ExecOption[Row] {
	structFieldIndexes := make([][]int, len(columnNames))

	for columnName, structField := range Fields(reflect.TypeOf(new(Row)).Elem()) {
		if columnIndex := slices.Index(columnNames, columnName); columnIndex >= 0 {
			structFieldIndexes[columnIndex] = structField.Index
		}
	}

	for i, structFieldIndex := range structFieldIndexes {
		if structFieldIndex == nil {
			panic(fmt.Errorf("column %q not found", columnNames[i]))
		}
	}

	return ExecArgs(func(args []any, row Row) []any {
		rowValue := reflect.ValueOf(row)
		for _, structFieldIndex := range structFieldIndexes {
			args = append(args, rowValue.FieldByIndex(structFieldIndex).Interface())
		}
		return args
	})
}

// ExecArgs is an option that specifies the function being called to generate
// the list of arguments passed when executing a query.
//
// By default, the Row value is converted to a list of arguments by taking the
// fields with a "sql" struct tag in the order they appear in the struct,
// as defined by the reflect.VisibleFields function.
//
// The function must append the arguments to the slice passed as argument and
// return the resulting slice.
func ExecArgs[Row any](fn func([]any, Row) []any) ExecOption[Row] {
	return func(opts *execOptions[Row]) { opts.args = fn }
}

// ExecQuery is an option that specifies the function being called to generate
// the query to execute for a given Row value.
//
// The function receives the original query value passed to Exec or ExecContext,
// and returns the query to execute.
//
// This is useful when parts of the query depend on the Row value that the query
// is being executed on, for example when the query is an insert.
func ExecQuery[Row any](fn func(string, Row) string) ExecOption[Row] {
	return func(opts *execOptions[Row]) { opts.query = fn }
}

type execOptions[Row any] struct {
	args  func([]any, Row) []any
	query func(string, Row) string
}

// Executable is the interface implemented by sql.DB, sql.Stmt, or sql.Tx.
type Executable interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Exec is like ExecContext but it uses the background context.
func Exec[Row any](e Executable, query string, seq iter.Seq2[Row, error], opts ...ExecOption[Row]) iter.Seq2[sql.Result, error] {
	return ExecContext[Row](context.Background(), e, query, seq, opts...)
}

// ExecContext executes a query for each row in the sequence.
//
// To ensure that the query is executed atomicatlly, it is usually useful to
// call ExecContext on a transaction (sql.Tx), for example:
//
//	tx, err := db.BeginTx(ctx, nil)
//	if err != nil {
//	  ...
//	}
//	defer tx.Rollback()
//	for r, err := range sqlrange.ExecContext[RowType](ctx, tx, query, rows) {
//	  if err != nil {
//	    ...
//	  }
//	  ...
//	}
//	if err := tx.Commit(); err != nil {
//	  ...
//	}
//
// Since the function makes one query execution for each row read from the
// sequence, latency of the query execution can quickly increase. In some cases,
// such as inserting values in a database, the program can amortize the cost of
// query latency by batching the rows being inserted, for example:
//
//	for r, err := range sqlrange.ExecContext(ctx, tx,
//		`insert into table (col1, col2, col3) values `,
//		// yield groups of rows to be inserted in bulk
//		func(yield func([]RowType, error) bool) {
//		  ...
//		},
//		// append values for the insert query
//		sqlrange.ExecArgs(func(args []any, rows []RowType) []any {
//		  for _, row := range rows {
//		    args = append(args, row.Col1, row.Col2, row.Col3)
//		  }
//		  return args
//		}),
//		// generate placeholders for the insert query
//		sqlrange.ExecQuery(func(query string, rows []RowType) string {
//		  return query + strings.Repeat(`(?, ?, ?)`, len(rows))
//		}),
//	) {
//		...
//	}
//
// Batching operations this way is necessary to achieve high throughput when
// inserting values into a database.
func ExecContext[Row any](ctx context.Context, e Executable, query string, seq iter.Seq2[Row, error], opts ...ExecOption[Row]) iter.Seq2[sql.Result, error] {
	return func(yield func(sql.Result, error) bool) {
		options := new(execOptions[Row])
		for _, opt := range opts {
			opt(options)
		}

		if options.args == nil {
			val := reflect.ValueOf(new(Row)).Elem()
			fields := Fields(val.Type())
			options.args = func(args []any, _ Row) []any {
				for _, structField := range fields {
					args = append(args, val.FieldByIndex(structField.Index).Interface())
				}
				return args
			}
		}

		if options.query == nil {
			options.query = func(query string, _ Row) string { return query }
		}

		var execArgs []any
		var execQuery string
		for r, err := range seq {
			if err != nil {
				yield(nil, err)
				return
			}
			execArgs = options.args(execArgs[:0], r)
			execQuery = options.query(query, r)

			res, err := e.ExecContext(ctx, execQuery, execArgs...)
			if !yield(res, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// Queryable is an interface implemented by types that can send SQL queries,
// such as *sql.DB, *sql.Stmt, or *sql.Tx.
type Queryable interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Query is like QueryContext but it uses the background context.
func Query[Row any](q Queryable, query string, args ...any) iter.Seq2[Row, error] {
	return QueryContext[Row](context.Background(), q, query, args...)
}

// QueryContext returns the results of the query as a sequence of rows.
//
// The returned function automatically closes the unerlying sql.Rows value when
// it completes its iteration. The function can only be iterated once, it will
// not retain the values that it has seen.
//
// A typical use of QueryContext is:
//
//	for row, err := range sqlrange.QueryContext[RowType](ctx, db, query, args...) {
//	  if err != nil {
//	    ...
//	  }
//	  ...
//	}
//
// The q parameter represents a queryable type, such as *sql.DB, *sql.Stmt,
// or *sql.Tx.
//
// See Scan for more information about how the rows are mapped to the row type
// parameter Row.
func QueryContext[Row any](ctx context.Context, q Queryable, query string, args ...any) iter.Seq2[Row, error] {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return func(yield func(Row, error) bool) {
			var zero Row
			yield(zero, err)
		}
	}
	return Scan[Row](rows)
}

// Scan returns a sequence of rows from a sql.Rows value.
//
// The returned function automatically closes the rows passed as argument when
// it completes its iteration. The function can only be iterated once, it will
// not retain the values that it has seen.
//
// A typical use of Scan is:
//
//	rows, err := db.QueryContext(ctx, query, args...)
//	if err != nil {
//	  ...
//	}
//	for row, err := range sqlrange.Scan[RowType](rows) {
//	  if err != nil {
//	    ...
//	  }
//	  ...
//	}
//
// Scan uses reflection to map the columns of the rows to the fields of the
// struct passed as argument. The mapping is done by matching the name of the
// columns with the name of the fields. The name of the columns is taken from
// the "sql" tag of the fields. For example:
//
//	type Row struct {
//	  ID   int64  `sql:"id"`
//	  Name string `sql:"name"`
//	}
//
// The fields of the struct that do not have a "sql" tag are ignored.
//
// Ranging over the returned function will panic if the type parameter is not a
// struct.
func Scan[Row any](rows *sql.Rows) iter.Seq2[Row, error] {
	return func(yield func(Row, error) bool) {
		defer rows.Close()
		var zero Row

		columns, err := rows.Columns()
		if err != nil {
			yield(zero, err)
			return
		}

		scanArgs := make([]any, len(columns))
		row := new(Row)
		val := reflect.ValueOf(row).Elem()

		for columnName, structField := range Fields(val.Type()) {
			if columnIndex := slices.Index(columns, columnName); columnIndex >= 0 {
				scanArgs[columnIndex] = val.FieldByIndex(structField.Index).Addr().Interface()
			}
		}

		for rows.Next() {
			if err := rows.Scan(scanArgs...); err != nil {
				yield(zero, err)
				return
			}
			if !yield(*row, nil) {
				return
			}
			*row = zero
		}

		if err := rows.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// Fields returns a sequence of the fields of a struct type that have a "sql"
// tag.
func Fields(t reflect.Type) iter.Seq2[string, reflect.StructField] {
	return func(yield func(string, reflect.StructField) bool) {
		cache, _ := cachedFields.Load().(map[reflect.Type][]field)

		fields, ok := cache[t]
		if !ok {
			fields = appendFields(nil, t)

			newCache := make(map[reflect.Type][]field, len(cache)+1)
			for k, v := range cache {
				newCache[k] = v
			}
			newCache[t] = fields
			cachedFields.Store(newCache)
		}

		for _, f := range fields {
			if !yield(f.name, f.field) {
				return
			}
		}
	}
}

type field struct {
	name  string
	field reflect.StructField
}

var cachedFields atomic.Value // map[reflect.Type][]field

func appendFields(fields []field, t reflect.Type) []field {
	for i, n := 0, t.NumField(); i < n; i++ {
		if f := t.Field(i); f.IsExported() {
			if f.Anonymous {
				if f.Type.Kind() == reflect.Struct {
					fields = appendFields(fields, f.Type)
				}
			} else if s, ok := f.Tag.Lookup("sql"); ok {
				fields = append(fields, field{s, f})
			}
		}
	}
	return fields
}
