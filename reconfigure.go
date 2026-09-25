package lidza

import (
	"context"
	"errors"
	"fmt"
)

// Reconfigurable is a pack that can read its configuration again while
// the app runs: after a credential is saved from the admin pages, the
// mail, llm and storage packs pick up the new provider or key without a
// restart.
type Reconfigurable interface {
	Reconfigure(ctx context.Context) error
}

// Reconfigure asks every service that can to read its configuration
// again. Every error is returned, joined.
func Reconfigure(ctx context.Context, s *Services) error {
	var errs []error
	s.Each(func(v any) {
		if r, ok := v.(Reconfigurable); ok {
			if err := r.Reconfigure(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T: %w", v, err))
			}
		}
	})
	return errors.Join(errs...)
}
