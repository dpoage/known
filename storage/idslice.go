package storage

import (
	"encoding/json"
	"fmt"

	"github.com/dpoage/known/model"
)

// MarshalIDSlice encodes a slice of model.IDs as a JSON string pointer.
// Returns nil for empty slices (stored as NULL in the database).
func MarshalIDSlice(ids []model.ID) (*string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	b, err := json.Marshal(strs)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

// UnmarshalIDSlice decodes a JSON string pointer into a slice of model.IDs.
// Returns nil for nil or empty input.
func UnmarshalIDSlice(data *string) ([]model.ID, error) {
	if data == nil || *data == "" {
		return nil, nil
	}
	var strs []string
	if err := json.Unmarshal([]byte(*data), &strs); err != nil {
		return nil, err
	}
	ids := make([]model.ID, len(strs))
	for i, s := range strs {
		id, err := model.ParseID(s)
		if err != nil {
			return nil, fmt.Errorf("parse id %q: %w", s, err)
		}
		ids[i] = id
	}
	return ids, nil
}
