# sqlrange [![Go Reference](https://pkg.go.dev/badge/github.com/achille-roussel/sqlrange.svg)](https://pkg.go.dev/github.com/achille-roussel/sqlrange)

[go1.22rc1]: https://gist.github.com/achille-roussel/5a9afe81c91891de4fad0bfe0965a9ea

Library using the `database/sql` package and Go 1.22 range functions to execute
queries against SQL databases.

## Installation

This package is intended to be used as a library and installed with:
```sh
go get github.com/achille-roussel/sqlrange
```

:warning: The package depends on Go 1.22 (currently in rc1 release) and
enabling the rangefunc experiment.

To download Go 1.22 rc1: https://pkg.go.dev/golang.org/dl/go1.22rc1
```
go install golang.org/dl/go1.22rc1@latest
go1.22rc1 download
```
Then to enable the rangefunc experiment, set the GOEXPERIMENT environment
variable in the shell that executes the go commands:
```sh
export GOEXPERIMENT=rangefunc
```

For a more detailed guide of how to configure Go 1.22 with range functions see
[Go 1.22 rc1 installation to enable the range functions experiment][go1.22rc1].

## Usage

The `sqlrange` package contains two kinds of functions called **Exec** and
**Query** which wrap the standard library's `database/sql` methods with the
same names. The package adds stronger type safety and the ability to use
range functions as iterators to pass values to the queries or retrieve results.

Note that `sqlrange` **IS NOT AN ORM**, it is a lightweight package which does
not hide any of the details and simply provides library functions to structure
applications that stream values in and out of databases.

### Query

The **Query** functions are used to read streams of values from databases,
in the same way that `sql.(*DB).Query` does, but using range functions to
simplify the code constructs, and type parameters to automatically decode
SQL results into Go struct values.

The type parameter must be a struct with fields containing "sql" struct tags
to define the names of columns that the fields are mapped to:
```go
type Point struct {
    X float64 `sql:"x"`
    Y float64 `sql:"y"`
}
```
```go
for p, err := range sqlrange.Query[Point](db, `select x, y from points`) {
    if err != nil {
        ...
    }
    ...
}
```
Note that resource management here is automated by the range function
returned by calling **Query**, the underlying `*sql.Rows` value is automatically
closed when the program exits the body of the range loop consuming the rows.

### Exec

The **Exec** functions are used to execute insert, update, or delete queries
against databases, accepting a stream of parameters as arguments (in the form of
a range function), and returning a stream of results.

Since the function will send multiple queries to the database, it is often
preferable to apply it to a transaction (or a statement derived from a
transaction via `sql.(*Tx).Stmt`) to ensure atomicity of the operation.

```go
tx, err := db.Begin()
if err != nil {
    ...
}
defer tx.Rollback()

for r, err := range sqlrange.Exec(tx,
    `insert into table (col1, col2, col3) values (?, ?, ?)`,
    // This range function yields the values that will be inserted into
    // the database by executing the query above.
    func(yield func(RowType, error) bool) {
        ...
    },
    // Inject the arguments for the SQL query being executed.
    // The function is called for each value yielded by the range
    // function above.
    sqlrange.ExecArgs(func(args []any, row RowType) []any {
        return append(args, row.Col1, row.Col2, row.Col3)
    }),
) {
    // Each results of each execution are streamed and must be consumed
    // by the program to drive the operation.
    if err != nil {
        ...
    }
    ...
}

if err := tx.Commit(); err != nil {
    ...
}
```
