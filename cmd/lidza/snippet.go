package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agim/lidza/pkg/snippets"
)

// runSnippet is `lidza snippet [name]`: a file of the reference app, or
// the catalog.
func runSnippet(_ context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(snippets.Catalog())
		return nil
	}
	sn, content, ok := snippets.Get(args[0])
	if !ok {
		return errors.New("no snippet " + args[0] + "; names: " + strings.Join(snippets.Names(), ", "))
	}
	fmt.Println("//", snippets.Header(sn))
	fmt.Print(content)
	return nil
}
