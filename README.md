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

### Context

Mirroring methods of the `sql.DB` type, functions of the `sqlrange` package have
variants that take a `context.Context` as first argument to support asynchronous
cancellation or timing out the operations.

Reusing the example above, we could set a 10 secondstime limit for the query
using **QueryContext** instead of **Query**:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

for p, err := range sqlrange.QueryContext[Point](ctx, db, `select x, y from points`) {
    if err != nil {
        ...
    }
    ...
}
```

The context is propagated to the `sql.(*DB).QueryContext` method, which then
passes it to the underlying SQL driver.

## Performance

Functions in this package are optimized to have a minimal compute and memory
footprint. Applications should not observe any performance degradation from
using it, compared to using the `database/sql` package directly. This is an
important property of the package since it means that the type safety, resource
lifecycle management, and expressiveness do not have to be a trade off.

This is a use case where the use of range functions really shines: because all
the code points where range functions are created get inlined, the compiler's
escape analysis can place most of the values on the stack, keeping the memory
and garbage collection overhead to a minimum.

Most of the escaping heap allocations in this package come from the use of
reflection to convert SQL rows into Go values, which are optimized using two
different approaches:

- **Caching:** internally, the package caches the `reflect.StructField` values
  that it needs. This is necessary to remove some of the allocations caused by
  the `reflect` package allocating the [`Index`][structField] on the heap.
  See https://github.com/golang/go/issues/2320 for more details.

- **Amortization:** since the intended use case is to select ranges of rows,
  or execute batch queries, the functions can reuse the local state maintained
  to read values. The more rows are involved in the query, the great the cost of
  allocating those values gets amortized, to the point that it quickly becomes
  insignificant.

To illustrate, we can look at the memory profiles for the package benchmarks.

**objects allocated on the heap**
```
File: sqlrange.test
Type: alloc_objects
Time: Jan 15, 2024 at 8:32am (PST)
Showing nodes accounting for 23444929, 97.50% of 24046152 total
Dropped 43 nodes (cum <= 120230)
      flat  flat%   sum%        cum   cum%
  21408835 89.03% 89.03%   21408835 89.03%  github.com/achille-roussel/sqlrange_test.(*fakeStmt).QueryContext /go/src/github.com/achille-roussel/sqlrange/fakedb_test.go:1040
   1769499  7.36% 96.39%    1769499  7.36%  strconv.formatBits /sdk/go1.22rc1/src/strconv/itoa.go:199
    217443   0.9% 97.30%     217443   0.9%  github.com/achille-roussel/sqlrange_test.(*fakeStmt).QueryContext /go/src/github.com/achille-roussel/sqlrange/fakedb_test.go:1044
     32768  0.14% 97.43%   21926303 91.18%  database/sql.(*DB).query /sdk/go1.22rc1/src/database/sql/sql.go:1754
     16384 0.068% 97.50%   23925181 99.50%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:145
         0     0% 97.50%   21926303 91.18%  database/sql.(*DB).QueryContext /sdk/go1.22rc1/src/database/sql/sql.go:1731
         0     0% 97.50%   21926303 91.18%  database/sql.(*DB).QueryContext.func1 /sdk/go1.22rc1/src/database/sql/sql.go:1732
         0     0% 97.50%   21746433 90.44%  database/sql.(*DB).queryDC /sdk/go1.22rc1/src/database/sql/sql.go:1806
         0     0% 97.50%   22039082 91.65%  database/sql.(*DB).retry /sdk/go1.22rc1/src/database/sql/sql.go:1566
         0     0% 97.50%    1769499  7.36%  database/sql.(*Rows).Scan /sdk/go1.22rc1/src/database/sql/sql.go:3354
         0     0% 97.50%    1769499  7.36%  database/sql.asString /sdk/go1.22rc1/src/database/sql/convert.go:499
         0     0% 97.50%    1769499  7.36%  database/sql.convertAssignRows /sdk/go1.22rc1/src/database/sql/convert.go:433
         0     0% 97.50%     169852  0.71%  database/sql.ctxDriverPrepare /sdk/go1.22rc1/src/database/sql/ctxutil.go:15
         0     0% 97.50%   21746433 90.44%  database/sql.ctxDriverStmtQuery /sdk/go1.22rc1/src/database/sql/ctxutil.go:82
         0     0% 97.50%   21746433 90.44%  database/sql.rowsiFromStatement /sdk/go1.22rc1/src/database/sql/sql.go:2836
         0     0% 97.50%     202620  0.84%  database/sql.withLock /sdk/go1.22rc1/src/database/sql/sql.go:3530
         0     0% 97.50%   21926303 91.18%  github.com/achille-roussel/sqlrange.QueryContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }] /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:213
         0     0% 97.50%    1769499  7.36%  github.com/achille-roussel/sqlrange.QueryContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].Scan[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].func2 /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:278
         0     0% 97.50%   21926303 91.18%  github.com/achille-roussel/sqlrange.Query[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }] /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:189 (inline)
         0     0% 97.50%     120971   0.5%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:129
         0     0% 97.50%     120971   0.5%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows.Exec[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].ExecContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].func4 /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:162
         0     0% 97.50%     120971   0.5%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows.func1 /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:131
         0     0% 97.50%    1769499  7.36%  strconv.FormatInt /sdk/go1.22rc1/src/strconv/itoa.go:29
         0     0% 97.50%   24033864 99.95%  testing.(*B).launch /sdk/go1.22rc1/src/testing/benchmark.go:316
         0     0% 97.50%   24046152   100%  testing.(*B).runN /sdk/go1.22rc1/src/testing/benchmark.go:193
```

**memory allocated on the heap**
```
File: sqlrange.test
Type: alloc_space
Time: Jan 15, 2024 at 8:32am (PST)
Showing nodes accounting for 626.05MB, 97.66% of 641.05MB total
Dropped 33 nodes (cum <= 3.21MB)
      flat  flat%   sum%        cum   cum%
  408.51MB 63.72% 63.72%   408.51MB 63.72%  github.com/achille-roussel/sqlrange_test.(*fakeStmt).QueryContext /go/src/github.com/achille-roussel/sqlrange/fakedb_test.go:1040
  174.04MB 27.15% 90.87%   174.04MB 27.15%  github.com/achille-roussel/sqlrange_test.(*fakeStmt).QueryContext /go/src/github.com/achille-roussel/sqlrange/fakedb_test.go:1044
      27MB  4.21% 95.09%       27MB  4.21%  strconv.formatBits /sdk/go1.22rc1/src/strconv/itoa.go:199
    5.50MB  0.86% 95.94%     5.50MB  0.86%  github.com/achille-roussel/sqlrange_test.(*fakeStmt).QueryContext /go/src/github.com/achille-roussel/sqlrange/fakedb_test.go:1064
    5.50MB  0.86% 96.80%     5.50MB  0.86%  database/sql.(*DB).queryDC /sdk/go1.22rc1/src/database/sql/sql.go:1815
    4.50MB   0.7% 97.50%     4.50MB   0.7%  strings.genSplit /sdk/go1.22rc1/src/strings/strings.go:249
    0.50MB 0.078% 97.58%   635.05MB 99.06%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:145
    0.50MB 0.078% 97.66%   602.05MB 93.92%  database/sql.(*DB).query /sdk/go1.22rc1/src/database/sql/sql.go:1754
         0     0% 97.66%     5.50MB  0.86%  database/sql.(*DB).ExecContext /sdk/go1.22rc1/src/database/sql/sql.go:1661
         0     0% 97.66%     5.50MB  0.86%  database/sql.(*DB).ExecContext.func1 /sdk/go1.22rc1/src/database/sql/sql.go:1662
         0     0% 97.66%   602.05MB 93.92%  database/sql.(*DB).QueryContext /sdk/go1.22rc1/src/database/sql/sql.go:1731
         0     0% 97.66%   602.05MB 93.92%  database/sql.(*DB).QueryContext.func1 /sdk/go1.22rc1/src/database/sql/sql.go:1732
         0     0% 97.66%     5.50MB  0.86%  database/sql.(*DB).exec /sdk/go1.22rc1/src/database/sql/sql.go:1683
         0     0% 97.66%        5MB  0.78%  database/sql.(*DB).queryDC /sdk/go1.22rc1/src/database/sql/sql.go:1797
         0     0% 97.66%   589.55MB 91.97%  database/sql.(*DB).queryDC /sdk/go1.22rc1/src/database/sql/sql.go:1806
         0     0% 97.66%        5MB  0.78%  database/sql.(*DB).queryDC.func2 /sdk/go1.22rc1/src/database/sql/sql.go:1798
         0     0% 97.66%   607.55MB 94.77%  database/sql.(*DB).retry /sdk/go1.22rc1/src/database/sql/sql.go:1566
         0     0% 97.66%       27MB  4.21%  database/sql.(*Rows).Scan /sdk/go1.22rc1/src/database/sql/sql.go:3354
         0     0% 97.66%       27MB  4.21%  database/sql.asString /sdk/go1.22rc1/src/database/sql/convert.go:499
         0     0% 97.66%       27MB  4.21%  database/sql.convertAssignRows /sdk/go1.22rc1/src/database/sql/convert.go:433
         0     0% 97.66%        8MB  1.25%  database/sql.ctxDriverPrepare /sdk/go1.22rc1/src/database/sql/ctxutil.go:15
         0     0% 97.66%   589.55MB 91.97%  database/sql.ctxDriverStmtQuery /sdk/go1.22rc1/src/database/sql/ctxutil.go:82
         0     0% 97.66%   589.55MB 91.97%  database/sql.rowsiFromStatement /sdk/go1.22rc1/src/database/sql/sql.go:2836
         0     0% 97.66%     8.50MB  1.33%  database/sql.withLock /sdk/go1.22rc1/src/database/sql/sql.go:3530
         0     0% 97.66%   602.05MB 93.92%  github.com/achille-roussel/sqlrange.QueryContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }] /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:213
         0     0% 97.66%       27MB  4.21%  github.com/achille-roussel/sqlrange.QueryContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].Scan[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].func2 /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:278
         0     0% 97.66%   602.05MB 93.92%  github.com/achille-roussel/sqlrange.Query[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }] /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:189 (inline)
         0     0% 97.66%        6MB  0.94%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:129
         0     0% 97.66%        6MB  0.94%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows.Exec[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].ExecContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].func4 /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:162
         0     0% 97.66%     5.50MB  0.86%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows.Exec[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].ExecContext[go.shape.struct { Age int "sql:\"age\""; Name string "sql:\"name\""; BirthDate time.Time "sql:\"bdate\"" }].func4.3 /go/src/github.com/achille-roussel/sqlrange/sqlrange.go:170
         0     0% 97.66%        6MB  0.94%  github.com/achille-roussel/sqlrange_test.BenchmarkQuery100Rows.func1 /go/src/github.com/achille-roussel/sqlrange/sqlrange_test.go:131
         0     0% 97.66%       27MB  4.21%  strconv.FormatInt /sdk/go1.22rc1/src/strconv/itoa.go:29
         0     0% 97.66%     4.50MB   0.7%  strings.Split /sdk/go1.22rc1/src/strings/strings.go:307 (inline)
         0     0% 97.66%   640.05MB 99.84%  testing.(*B).launch /sdk/go1.22rc1/src/testing/benchmark.go:316
         0     0% 97.66%   641.05MB   100%  testing.(*B).runN /sdk/go1.22rc1/src/testing/benchmark.go:193
```

Almost all the memory allocated on the heap is done in the SQL driver.
The fake driver employed for tests isn't very efficient, but it still shows
that the package does not contribute to the majority of memory usage.
Programs that use SQL drivers for production databases like MySQL or Postgres
will have performance characteristics dictated by the driver and won't suffer
from utilizing the `sqlrange` package abstractions.

[structField]: https://pkg.go.dev/reflect#StructField
