package promise

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
	conc "github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

var (
	ctx         = context.Background()
	errExpected = errors.New("expected error")
)

func TestNew(t *testing.T) {
	p := New(func(resolve func(any), reject func(error)) {
		resolve(nil)
	})
	require.NotNil(t, p)
}

func TestNewWithPool(t *testing.T) {
	tests := []struct {
		name string
		pool Pool
	}{
		{
			name: "default",
			pool: newDefaultPool(),
		},
		{
			name: "conc",
			pool: func() Pool {
				return FromConcPool(conc.New())
			}(),
		},
		{
			name: "ants",
			pool: func() Pool {
				antsPool, err := ants.NewPool(0)
				require.NoError(t, err)
				return FromAntsPool(antsPool)
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := NewWithPool(func(resolve func(string), reject func(error)) {
				resolve(test.name)
			}, test.pool)

			val, err := p.Await(ctx)
			require.NoError(t, err)
			require.NotNil(t, val)
			require.Equal(t, test.name, *val)
		})
	}
}

func TestPromise_Then(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		resolve("Hello, ")
	})
	p2 := Then(p1, ctx, func(data string) (string, error) {
		return data + "world!", nil
	})
	p3 := Then(p2, ctx, func(_ string) (string, error) {
		return "", errExpected
	})

	val, err := p1.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Equal(t, "Hello, ", *val)

	val, err = p2.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Equal(t, "Hello, world!", *val)

	_, err = p3.Await(ctx)
	require.EqualError(t, err, errExpected.Error())
}

func TestPromise_Catch(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	val, err := p1.Await(ctx)
	require.Error(t, err)
	require.Equal(t, errExpected, err)
	require.Nil(t, val)
}

func TestPromise_Panic(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		panic("random error")
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		panic(errExpected)
	})

	val, err := p1.Await(ctx)
	require.Error(t, err)
	require.Equal(t, errors.New("random error"), err)
	require.Nil(t, val)

	val, err = p2.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestAll_Happy(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		resolve("one")
	})
	p2 := New(func(resolve func(string), reject func(error)) {
		resolve("two")
	})
	p3 := New(func(resolve func(string), reject func(error)) {
		resolve("three")
	})

	p := All(ctx, p1, p2, p3)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Equal(t, []string{"one", "two", "three"}, *val)
}

func TestAll_ContainsRejected(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		resolve("one")
	})
	p2 := New(func(resolve func(string), reject func(error)) {
		reject(errExpected)
	})
	p3 := New(func(resolve func(string), reject func(error)) {
		resolve("three")
	})

	p := All(ctx, p1, p2, p3)

	val, err := p.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestAll_OnlyRejected(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})
	p3 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	p := All(ctx, p1, p2, p3)

	val, err := p.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestRace_Happy(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 100)
		resolve("faster")
	})
	p2 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 500)
		resolve("slower")
	})

	p := Race(ctx, p1, p2)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Equal(t, "faster", *val)
}

func TestRace_ContainsRejected(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		time.Sleep(time.Millisecond * 100)
		resolve(nil)
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	p := Race(ctx, p1, p2)

	val, err := p.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestRace_OnlyRejected(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	p := Race(ctx, p1, p2)

	val, err := p.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestFirst_Happy(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 250)
		resolve("faster")
	})
	p2 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 500)
		resolve("slower")
	})
	p3 := New(func(resolve func(string), reject func(error)) {
		reject(errExpected)
	})

	p := First(ctx, p3, p2, p1)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.Equal(t, "faster", *val)
}

func TestFirst_OnlyRejected(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	p := First(ctx, p1, p2)

	val, err := p.Await(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, errExpected)
	require.Nil(t, val)
}

func TestAllResolved_Happy(t *testing.T) {
	p1 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 500)
		resolve("slower")
	})
	p2 := New(func(resolve func(string), reject func(error)) {
		time.Sleep(time.Millisecond * 250)
		resolve("faster")
	})
	p3 := New(func(resolve func(string), reject func(error)) {
		reject(errExpected)
	})

	p := AllResolved(ctx, p3, p2, p1)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Len(t, *val, 2)
	require.Equal(t, "faster", (*val)[0])
	require.Equal(t, "slower", (*val)[1])
}

func TestAllResolved_OnlyRejected(t *testing.T) {
	p1 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})
	p2 := New(func(resolve func(any), reject func(error)) {
		reject(errExpected)
	})

	p := AllResolved(ctx, p1, p2)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Empty(t, *val)
}

func TestAllResolved_LimitConcurrency(t *testing.T) {
	var pool Pool

	var promises []*Promise[any]

	// conc pool
	{
		concPool := conc.New().
			WithMaxGoroutines(10)
		pool = FromConcPool(concPool)
	}

	// ants pool
	if false {
		antsPool, err := ants.NewPool(10)
		if err != nil {
			require.NoError(t, err)
		}
		pool = FromAntsPool(antsPool)
	}

	// default pool (goroutines)
	if false {
		pool = DefaultPool
	}

	for i := 0; i < 100; i++ {
		i := i
		promises = append(promises, NewWithPool(func(resolve func(any), reject func(error)) {
			time.Sleep(100 * time.Millisecond)
			resolve(i)
		}, pool))
	}

	p := AllResolvedWithPool(ctx, pool, promises...)

	val, err := p.Await(ctx)
	require.NoError(t, err)
	require.NotNil(t, val)
	require.Len(t, *val, 100)
}

func TestXxx(t *testing.T) {
	ctx := context.Background()

	p := New(func(resolve func(string), reject func(error)) {
		time.Sleep(1 * time.Second)
		resolve("a")
	})

	res, err := p.Await(ctx)

	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "a", *res)
}

func TestBatch(t *testing.T) {
	var pool Pool

	// conc pool
	{
		concPool := conc.New().
			WithMaxGoroutines(1)
		pool = FromConcPool(concPool)
	}

	// ants pool
	if false {
		antsPool, err := ants.NewPool(10)
		if err != nil {
			require.NoError(t, err)
		}
		pool = FromAntsPool(antsPool)
	}

	// default pool (goroutines)
	if false {
		pool = DefaultPool
	}

	ctx := context.Background()

	var promises []*Promise[string]

	for i := 0; i < 24; i++ {

		i := i

		promises = append(promises, New(func(resolve func(string), reject func(error)) {
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			resolve(fmt.Sprintf("=> %d\n", i))
		}))
	}

	processed := 0

	for each := range BatchWithPool(ctx, pool, 5, promises...) {

		res, err := each.Await(ctx)

		require.NoError(t, err)
		require.NotNil(t, res)

		require.LessOrEqual(t, len(*res), 5)

		processed += len(*res)

		t.Log(*res)
	}

	require.Equal(t, 24, processed)

}

func TestBatchWithThen(t *testing.T) {
	ctx := context.Background()

	var promises []*Promise[string]
	var promises2 []*Promise[string]

	for i := 0; i < 24; i++ {

		i := i

		promises = append(promises, New(func(resolve func(string), reject func(error)) {
			time.Sleep(time.Duration(rand.Intn(100)+100) * time.Millisecond)
			t.Log("promise1")
			resolve(fmt.Sprintf("=> %d", i))
		}))
	}

	for _, each := range promises {
		promises2 = append(promises2, Then(each, ctx, func(data string) (string, error) {
			time.Sleep(time.Duration(rand.Intn(100)+100) * time.Millisecond)
			t.Log("promise2")
			return data + " modified", nil
		}))
	}

	processed := 0

	for each := range Batch(ctx, 5, promises2...) {

		res, err := each.Await(ctx)

		require.NoError(t, err)
		require.NotNil(t, res)

		require.LessOrEqual(t, len(*res), 5)

		processed += len(*res)

		t.Log(*res)

		for _, each := range *res {
			require.Contains(t, each, "modified")
		}
	}

	require.Equal(t, 24, processed)

}
