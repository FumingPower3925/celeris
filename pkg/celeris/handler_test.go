package celeris

import (
	"context"
	"errors"
	"testing"

	"github.com/albertbausili/celeris/internal/h2/stream"
)

func TestHandlerFunc_ServeHTTP2(t *testing.T) {
	called := false
	handler := HandlerFunc(func(_ *Context) error {
		called = true
		return nil
	})

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := handler.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !called {
		t.Error("Expected handler to be called")
	}
}

func TestHandlerFunc_Error(t *testing.T) {
	expectedErr := errors.New("test error")
	handler := HandlerFunc(func(_ *Context) error {
		return expectedErr
	})

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := handler.ServeHTTP2(ctx)
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}

func TestMiddlewareFunc_ToMiddleware(t *testing.T) {
	middlewareCalled := false
	handlerCalled := false

	middlewareFunc := MiddlewareFunc(func(ctx *Context, next Handler) error {
		middlewareCalled = true
		return next.ServeHTTP2(ctx)
	})

	handler := HandlerFunc(func(_ *Context) error {
		handlerCalled = true
		return nil
	})

	middleware := middlewareFunc.ToMiddleware()
	wrapped := middleware(handler)

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := wrapped.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !middlewareCalled {
		t.Error("Expected middleware to be called")
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestChain_Single(t *testing.T) {
	middlewareCalled := false
	handlerCalled := false

	middleware := func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			middlewareCalled = true
			return next.ServeHTTP2(ctx)
		})
	}

	handler := HandlerFunc(func(_ *Context) error {
		handlerCalled = true
		return nil
	})

	chained := Chain(middleware)(handler)

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := chained.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !middlewareCalled {
		t.Error("Expected middleware to be called")
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestChain_Multiple(t *testing.T) {
	var order []int

	middleware1 := func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			order = append(order, 1)
			err := next.ServeHTTP2(ctx)
			order = append(order, 4)
			return err
		})
	}

	middleware2 := func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			order = append(order, 2)
			err := next.ServeHTTP2(ctx)
			order = append(order, 5)
			return err
		})
	}

	handler := HandlerFunc(func(_ *Context) error {
		order = append(order, 3)
		return nil
	})

	chained := Chain(middleware1, middleware2)(handler)

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := chained.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	expected := []int{1, 2, 3, 5, 4}
	if len(order) != len(expected) {
		t.Errorf("Expected order length %d, got %d", len(expected), len(order))
	}

	for i, v := range expected {
		if order[i] != v {
			t.Errorf("Expected order[%d] = %d, got %d", i, v, order[i])
		}
	}
}

func TestChain_Empty(t *testing.T) {
	handlerCalled := false

	handler := HandlerFunc(func(_ *Context) error {
		handlerCalled = true
		return nil
	})

	chained := Chain()(handler)

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := chained.ServeHTTP2(ctx)
	if err != nil {
		t.Errorf("ServeHTTP2() error = %v", err)
	}

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}

func TestChain_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("test error")

	middleware := func(next Handler) Handler {
		return HandlerFunc(func(ctx *Context) error {
			return next.ServeHTTP2(ctx)
		})
	}

	handler := HandlerFunc(func(_ *Context) error {
		return expectedErr
	})

	chained := Chain(middleware)(handler)

	s := stream.NewStream(1)
	ctx := newContext(context.Background(), s, nil)

	err := chained.ServeHTTP2(ctx)
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}
