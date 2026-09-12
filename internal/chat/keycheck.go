package chat

import (
	"context"
	"errors"
	"fmt"
)

// ErrKeyRefused is an endpoint refusing a key it was asked to check.
var ErrKeyRefused = errors.New("the API refused the key")

// CheckKey asks an endpoint whether it takes a key, by listing its models --
// the one request every wire has that costs nothing and needs the key. It
// reports nil where the key was taken, an error wrapping ErrKeyRefused where it
// was refused, and any other error where the endpoint could not be asked.
//
// The refusal carries the status and not the vendor's words, which for some
// vendors quote part of the key back.
func CheckKey(ctx context.Context, wire, baseURL, key string) error {
	w, err := NewWire(wire, baseURL, key)
	if err != nil {
		return err
	}
	lister, ok := w.(modelLister)
	if !ok {
		return nil
	}
	_, err = lister.ListModels(ctx)
	var e *apiError
	if err != nil && refusedKey(err) && errors.As(err, &e) {
		return fmt.Errorf("%w (%s)", ErrKeyRefused, e.Status)
	}
	return err
}
