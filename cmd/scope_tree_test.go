package cmd

import (
	"bytes"
	"testing"

	"github.com/dpoage/known/model"
)

func TestPrintTree(t *testing.T) {
	tests := []struct {
		name         string
		scopes       []model.Scope
		currentScope string
		want         string
	}{
		{
			name: "single root",
			scopes: []model.Scope{
				{Path: "myproject"},
			},
			want: "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nmyproject\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "root with children",
			scopes: []model.Scope{
				{Path: "myproject"},
				{Path: "myproject.cmd"},
				{Path: "myproject.model"},
				{Path: "myproject.storage"},
			},
			want: "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nmyproject\n├── cmd\n├── model\n└── storage\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "nested children",
			scopes: []model.Scope{
				{Path: "myproject"},
				{Path: "myproject.cmd"},
				{Path: "myproject.storage"},
				{Path: "myproject.storage.sqlite"},
			},
			want: "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nmyproject\n├── cmd\n└── storage\n    └── sqlite\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "current scope annotated",
			scopes: []model.Scope{
				{Path: "myproject"},
				{Path: "myproject.cmd"},
				{Path: "myproject.storage"},
				{Path: "myproject.storage.sqlite"},
			},
			currentScope: "myproject.cmd",
			want:         "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nmyproject\n├── cmd  <-- you are here\n└── storage\n    └── sqlite\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "root scope not annotated",
			scopes: []model.Scope{
				{Path: "myproject"},
				{Path: "myproject.cmd"},
			},
			currentScope: model.RootScope,
			want:         "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nmyproject\n└── cmd\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "multiple roots",
			scopes: []model.Scope{
				{Path: "projecta"},
				{Path: "projecta.api"},
				{Path: "projectb"},
				{Path: "projectb.web"},
			},
			want: "Scopes defined — use /recall '<topic>' to check for stored knowledge:\nprojecta\n└── api\nprojectb\n└── web\nExample: /recall '<topic>' --scope <scope>\n",
		},
		{
			name: "deep nesting with siblings",
			scopes: []model.Scope{
				{Path: "app"},
				{Path: "app.backend"},
				{Path: "app.backend.api"},
				{Path: "app.backend.db"},
				{Path: "app.frontend"},
			},
			currentScope: "app.backend.api",
			want:         "Scopes defined — use /recall '<topic>' to check for stored knowledge:\napp\n├── backend\n│   ├── api  <-- you are here\n│   └── db\n└── frontend\nExample: /recall '<topic>' --scope <scope>\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			p := NewPrinter(&buf, false, false)
			printTree(p, tt.scopes, tt.currentScope)
			got := buf.String()
			if got != tt.want {
				t.Errorf("printTree() =\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
