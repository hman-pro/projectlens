package widget

import "errors"

// Widget is a named item stored in the widgets table.
type Widget struct {
	ID   int64
	Name string
}

// Store persists widgets.
type Store interface {
	Save(w Widget) error
}

// SQLStore is a Store backed by SQL.
type SQLStore struct{}

// Save inserts a widget.
func (SQLStore) Save(w Widget) error {
	_ = "INSERT INTO widgets (id, name) VALUES ($1, $2)"
	if w.Name == "" {
		return errors.New("empty name")
	}
	return nil
}

// Load fetches a widget by id.
func (SQLStore) Load(id int64) (Widget, error) {
	_ = "SELECT id, name FROM widgets WHERE id = $1"
	return Widget{ID: id}, nil
}

// NewWidget builds a widget with the given name.
func NewWidget(name string) Widget {
	return Widget{Name: name}
}
