package promise

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
)

// Promise represents the eventual completion (or failure) of an asynchronous operation and its resulting value
type Promise[T any] struct {
	value *T
	err   error
	ch    chan struct{}
	once  sync.Once
}

func New[T any](
	executor func(resolve func(T), reject func(error)),
) *Promise[T] {
	return NewWithPool(executor, DefaultPool)
}

func NewWithPool[T any](
	executor func(resolve func(T), reject func(error)),
	pool Pool,
) *Promise[T] {
	if executor == nil {
		panic("executor is nil")
	}
	if pool == nil {
		panic("pool is nil")
	}

	p := &Promise[T]{
		value: nil,
		err:   nil,
		ch:    make(chan struct{}),
		once:  sync.Once{},
	}

	pool.Go(func() {
		defer p.handlePanic()
		executor(p.resolve, p.reject)
	})

	return p
}

func Then[A, B any](
	p *Promise[A],
	ctx context.Context,
	resolve func(A) (B, error),
) *Promise[B] {
	return ThenWithPool(p, ctx, resolve, DefaultPool)
}

func ThenWithPool[A, B any](
	p *Promise[A],
	ctx context.Context,
	resolve func(A) (B, error),
	pool Pool,
) *Promise[B] {
	return NewWithPool(func(resolveB func(B), reject func(error)) {
		result, err := p.Await(ctx)
		if err != nil {
			reject(err)
			return
		}

		resultB, err := resolve(*result)
		if err != nil {
			reject(err)
			return
		}

		resolveB(resultB)
	}, pool)
}

func Catch[T any](
	p *Promise[T],
	ctx context.Context,
	reject func(err error) error,
) *Promise[T] {
	return CatchWithPool(p, ctx, reject, DefaultPool)
}

func CatchWithPool[T any](
	p *Promise[T],
	ctx context.Context,
	reject func(err error) error,
	pool Pool,
) *Promise[T] {
	return NewWithPool(func(resolve func(T), internalReject func(error)) {
		result, err := p.Await(ctx)
		if err != nil {
			internalReject(reject(err))
		} else {
			resolve(*result)
		}
	}, pool)
}

func (p *Promise[T]) Await(ctx context.Context) (*T, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ch:
		return p.value, p.err
	}
}

func (p *Promise[T]) resolve(value T) {
	p.once.Do(func() {
		p.value = &value
		close(p.ch)
	})
}

func (p *Promise[T]) reject(err error) {
	p.once.Do(func() {
		p.err = err
		close(p.ch)
	})
}

func (p *Promise[T]) handlePanic() {
	err := recover()
	if err == nil {
		return
	}

	switch v := err.(type) {
	case error:
		p.reject(v)
	default:
		p.reject(fmt.Errorf("%+v", v))
	}
}

// All resolves when all promises have resolved, or rejects immediately upon any of the promises rejecting
func All[T any](
	ctx context.Context,
	promises ...*Promise[T],
) *Promise[[]T] {
	return AllWithPool(ctx, DefaultPool, promises...)
}

func AllWithPool[T any](
	ctx context.Context,
	pool Pool,
	promises ...*Promise[T],
) *Promise[[]T] {
	if len(promises) == 0 {
		panic("missing promises")
	}

	return NewWithPool(func(resolve func([]T), reject func(error)) {
		resultsChan := make(chan tuple[T, int], len(promises))
		errsChan := make(chan error, len(promises))

		for idx, p := range promises {
			idx := idx
			_ = ThenWithPool(p, ctx, func(data T) (T, error) {
				resultsChan <- tuple[T, int]{_1: data, _2: idx}
				return data, nil
			}, pool)
			_ = CatchWithPool(p, ctx, func(err error) error {
				errsChan <- err
				return err
			}, pool)
		}

		results := make([]T, len(promises))
		for idx := 0; idx < len(promises); idx++ {
			select {
			case result := <-resultsChan:
				results[result._2] = result._1
			case err := <-errsChan:
				reject(err)
				return
			}
		}
		resolve(results)
	}, pool)
}

// Race resolves or rejects as soon as any one of the promises resolves or rejects
func Race[T any](
	ctx context.Context,
	promises ...*Promise[T],
) *Promise[T] {
	return RaceWithPool(ctx, DefaultPool, promises...)
}

func RaceWithPool[T any](
	ctx context.Context,
	pool Pool,
	promises ...*Promise[T],
) *Promise[T] {
	if len(promises) == 0 {
		panic("missing promises")
	}

	return NewWithPool(func(resolve func(T), reject func(error)) {
		valsChan := make(chan T, len(promises))
		errsChan := make(chan error, len(promises))

		for _, p := range promises {
			_ = ThenWithPool(p, ctx, func(data T) (T, error) {
				valsChan <- data
				return data, nil
			}, pool)
			_ = CatchWithPool(p, ctx, func(err error) error {
				errsChan <- err
				return err
			}, pool)
		}

		select {
		case val := <-valsChan:
			resolve(val)
		case err := <-errsChan:
			reject(err)
		}
	}, pool)
}

// First resolves when the first promise resolves or rejects with all rejected promises
func First[T any](
	ctx context.Context,
	promises ...*Promise[T],
) *Promise[T] {
	return FirstWithPool(ctx, DefaultPool, promises...)
}

func FirstWithPool[T any](
	ctx context.Context,
	pool Pool,
	promises ...*Promise[T],
) *Promise[T] {
	if len(promises) == 0 {
		panic("missing promises")
	}

	return NewWithPool(func(resolve func(T), reject func(error)) {
		valsChan := make(chan T, 1)
		errsChan := make(chan tuple[error, int], len(promises))

		for idx, p := range promises {
			idx := idx // https://golang.org/doc/faq#closures_and_goroutines
			_ = ThenWithPool(p, ctx, func(data T) (T, error) {
				valsChan <- data
				return data, nil
			}, pool)
			_ = CatchWithPool(p, ctx, func(err error) error {
				errsChan <- tuple[error, int]{_1: err, _2: idx}
				return err
			}, pool)
		}

		errs := make([]error, len(promises))
		for idx := 0; idx < len(promises); idx++ {
			select {
			case val := <-valsChan:
				resolve(val)
				return
			case err := <-errsChan:
				errs[err._2] = err._1
			}
		}

		errCombo := errs[0]
		for _, err := range errs[1:] {
			errCombo = errors.Wrap(err, errCombo.Error())
		}
		reject(errCombo)
	}, pool)
}

// AllResolved resolves with all resolved promises, ignoring all rejected promises
func AllResolved[T any](
	ctx context.Context,
	promises ...*Promise[T],
) *Promise[[]T] {
	return AllResolvedWithPool(ctx, DefaultPool, promises...)
}

func AllResolvedWithPool[T any](
	ctx context.Context,
	pool Pool,
	promises ...*Promise[T],
) *Promise[[]T] {
	if len(promises) == 0 {
		panic("missing promises")
	}

	return NewWithPool(func(resolve func([]T), reject func(error)) {
		resultsChan := make(chan tuple[T, int], len(promises))
		errsChan := make(chan error, len(promises))

		for idx, p := range promises {
			idx := idx
			_ = ThenWithPool(p, ctx, func(data T) (T, error) {
				resultsChan <- tuple[T, int]{_1: data, _2: idx}
				return data, nil
			}, pool)
			_ = CatchWithPool(p, ctx, func(err error) error {
				errsChan <- err
				return err
			}, pool)
		}

		results := make([]T, len(promises))
		onlyResults := make([]T, 0)
		for idx := 0; idx < len(promises); idx++ {
			select {
			case result := <-resultsChan:
				results[result._2] = result._1
				onlyResults = append(onlyResults, result._1)
			case <-errsChan:
			}
		}

		resolve(onlyResults)
	}, pool)
}

type tuple[T1, T2 any] struct {
	_1 T1
	_2 T2
}
