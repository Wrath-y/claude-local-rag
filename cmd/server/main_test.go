package main

import (
	"context"
	"errors"
	"testing"
)

func TestMainCompiles(t *testing.T) {
	// This test just verifies the main package compiles without error.
	// Integration testing requires the full sidecar + model setup.
	t.Log("main package compiles successfully")
}

func TestServerAddressIsLoopbackOnly(t *testing.T) {
	if address := serverAddress(8765); address != "127.0.0.1:8765" {
		t.Fatalf("server address=%q", address)
	}
}

type shutdownRecorder struct {
	events      *[]string
	shutdownErr error
}

func (r shutdownRecorder) Shutdown(context.Context) error {
	*r.events = append(*r.events, "http")
	return r.shutdownErr
}
func (r shutdownRecorder) Close()            { *r.events = append(*r.events, "close") }
func (r shutdownRecorder) Stop()             { *r.events = append(*r.events, "sidecar") }
func (r shutdownRecorder) CloseStore() error { *r.events = append(*r.events, "store"); return nil }

type shutdownStoreRecorder struct{ shutdownRecorder }

func (r shutdownStoreRecorder) Close() error { return r.CloseStore() }

func TestShutdownOrderedStopsAdmissionBeforeWorkersAndStore(t *testing.T) {
	events := []string{}
	graph := shutdownRecorder{events: &events}
	other := shutdownRecorder{events: &events}
	if err := shutdownOrdered(context.Background(), shutdownRecorder{events: &events}, graph, other, shutdownRecorder{events: &events}, shutdownStoreRecorder{shutdownRecorder{events: &events}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"http", "close", "close", "sidecar", "store"}
	if len(events) != len(want) {
		t.Fatalf("events=%v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}

func TestShutdownOrderedStillClosesWorkersAfterHTTPFailure(t *testing.T) {
	events := []string{}
	errHTTP := errors.New("http shutdown")
	err := shutdownOrdered(context.Background(), shutdownRecorder{events: &events, shutdownErr: errHTTP}, shutdownRecorder{events: &events}, shutdownRecorder{events: &events}, shutdownRecorder{events: &events}, shutdownStoreRecorder{shutdownRecorder{events: &events}})
	if !errors.Is(err, errHTTP) || len(events) != 5 || events[0] != "http" || events[4] != "store" {
		t.Fatalf("err=%v events=%v", err, events)
	}
}
