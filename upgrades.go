// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"context"
	"database/sql"
	"fmt"
)

// Exec returns an UpdateRule apply function that executes the specified
// statements sequentially.
func Exec(stmts ...string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for i, stmt := range stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("stmt %d: %w", i+1, err)
			}
		}
		return nil
	}
}

// NoAction is a no-op update action.
func NoAction(context.Context, *sql.Tx) error { return nil }
