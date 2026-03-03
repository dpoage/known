package storage

import (
	"testing"

	"github.com/dpoage/known/model"
)

func TestMarshalIDSlice_Empty(t *testing.T) {
	got, err := MarshalIDSlice(nil)
	if err != nil {
		t.Fatalf("MarshalIDSlice(nil): %v", err)
	}
	if got != nil {
		t.Errorf("MarshalIDSlice(nil) = %v, want nil", got)
	}
}

func TestMarshalUnmarshalIDSlice_RoundTrip(t *testing.T) {
	ids := []model.ID{model.NewID(), model.NewID()}

	marshaled, err := MarshalIDSlice(ids)
	if err != nil {
		t.Fatalf("MarshalIDSlice: %v", err)
	}
	if marshaled == nil {
		t.Fatal("MarshalIDSlice returned nil for non-empty slice")
	}

	got, err := UnmarshalIDSlice(marshaled)
	if err != nil {
		t.Fatalf("UnmarshalIDSlice: %v", err)
	}

	if len(got) != len(ids) {
		t.Fatalf("len = %d, want %d", len(got), len(ids))
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Errorf("id[%d] = %s, want %s", i, got[i], ids[i])
		}
	}
}

func TestUnmarshalIDSlice_Nil(t *testing.T) {
	got, err := UnmarshalIDSlice(nil)
	if err != nil {
		t.Fatalf("UnmarshalIDSlice(nil): %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestUnmarshalIDSlice_Empty(t *testing.T) {
	empty := ""
	got, err := UnmarshalIDSlice(&empty)
	if err != nil {
		t.Fatalf("UnmarshalIDSlice(empty): %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestUnmarshalIDSlice_MalformedJSON(t *testing.T) {
	bad := "not json"
	_, err := UnmarshalIDSlice(&bad)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestUnmarshalIDSlice_InvalidID(t *testing.T) {
	bad := `["not-a-valid-ulid"]`
	_, err := UnmarshalIDSlice(&bad)
	if err == nil {
		t.Error("expected error for invalid ID, got nil")
	}
}
