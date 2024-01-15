package sqlrange_test

import (
	"fmt"
	"log"
	"slices"
	"testing"
	"time"

	"github.com/achille-roussel/sqlrange"
)

func ExampleExec() {
	type Row struct {
		Age  int    `sql:"age"`
		Name string `sql:"name"`
	}

	db := newTestDB(new(testing.T), "people")
	defer db.Close()

	for res, err := range sqlrange.Exec(db, `INSERT|people|name=?,age=?`,
		func(yield func(Row, error) bool) {
			_ = yield(Row{Age: 19, Name: "Luke"}, nil) &&
				yield(Row{Age: 42, Name: "Hitchhiker"}, nil)
		},
		sqlrange.ExecArgsFields[Row]("name", "age"),
	) {
		if err != nil {
			log.Fatal(err)
		}
		rowsAffected, err := res.RowsAffected()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(rowsAffected)
	}

	// Output:
	// 1
	// 1
}

func ExampleQuery() {
	type Row struct {
		Age  int    `sql:"age"`
		Name string `sql:"name"`
	}

	db := newTestDB(new(testing.T), "people")
	defer db.Close()

	for row, err := range sqlrange.Query[Row](db, `SELECT|people|age,name|`) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(row)
	}

	// Output:
	// {1 Alice}
	// {2 Bob}
	// {3 Chris}
}

type person struct {
	Age       int       `sql:"age"`
	Name      string    `sql:"name"`
	BirthDate time.Time `sql:"bdate"`
}

func TestExec(t *testing.T) {
	db := newTestDB(t, "people")
	defer db.Close()

	for res, err := range sqlrange.Exec(db, `INSERT|people|name=?,age=?`,
		func(yield func(person, error) bool) {
			for _, p := range []person{
				{Age: 19, Name: "Luke"},
				{Age: 42, Name: "Hitchhiker"},
			} {
				if !yield(p, nil) {
					return
				}
			}
		},
		sqlrange.ExecArgsFields[person]("name", "age"),
	) {
		if err != nil {
			t.Fatal(err)
		}
		if n, err := res.RowsAffected(); err != nil {
			t.Fatal(err)
		} else if n != 1 {
			t.Errorf("expect 1, got %d", n)
		}
	}
}

func TestQuery(t *testing.T) {
	db := newTestDB(t, "people")
	defer db.Close()

	var people []person
	for p, err := range sqlrange.Query[person](db, `SELECT|people|age,name|`) {
		if err != nil {
			t.Fatal(err)
		}
		people = append(people, p)
	}

	expect := []person{
		{Age: 1, Name: "Alice"},
		{Age: 2, Name: "Bob"},
		{Age: 3, Name: "Chris"},
	}

	if !slices.Equal(people, expect) {
		t.Errorf("expect %v, got %v", expect, people)
	}
}

func BenchmarkQuery100Rows(b *testing.B) {
	const N = 500

	db := newTestDB(b, "people")
	defer db.Close()

	for _, err := range sqlrange.Exec(db, `INSERT|people|age=?,name=?,bdate=?`, func(yield func(person, error) bool) {
		for i := range N {
			if !yield(person{
				Age:  i,
				Name: fmt.Sprintf("Person %d", i),
			}, nil) {
				break
			}
		}
	}) {
		if err != nil {
			b.Fatal(err)
		}
	}

	for n := b.N; n > 0; {
		for _, err := range sqlrange.Query[person](db, `SELECT|people|age|`) {
			if err != nil {
				b.Fatal(err)
			}
			if n--; n == 0 {
				break
			}
		}
	}
}
