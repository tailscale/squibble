// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"context"
	"fmt"
)

// Exec returns an UpdateRule apply function that executes the specified
// statements sequentially.
func Exec(stmts ...string) func(context.Context, DBConn) error {
	return func(ctx context.Context, db DBConn) error {
		for i, stmt := range stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("stmt %d: %w", i+1, err)
			}
		}
		return nil
	}
}

// NoAction is a no-op update action.
func NoAction(context.Context, DBConn) error { return nil }
