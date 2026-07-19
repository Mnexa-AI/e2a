// e2a-openapi-sdk-check protects generated SDK names that wire-level OpenAPI
// compatibility tools do not compare. It is an internal build tool, not a
// shipped command.
package main

import (
	"fmt"
	"os"

	"github.com/tokencanopy/e2a/internal/openapicompat"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <base.yaml> <revision.yaml>\n", os.Args[0])
		os.Exit(2)
	}
	base, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open base: %v\n", err)
		os.Exit(1)
	}
	defer base.Close()
	revision, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open revision: %v\n", err)
		os.Exit(1)
	}
	defer revision.Close()
	// Product explicitly reclassified the gate/scan review boundary as beta.
	// This exact component set mirrors api/oasdiff-ignore-errors.txt; every
	// other stable SDK schema remains protected by the default freeze.
	allowedSchemaDemotions := []string{
		"ApproveRequest", "HoldReasonView", "PageReviewView", "ProtectionFindingView",
		"RejectRequest", "RejectResultView", "ReviewView", "ThreatCategoryView",
	}
	if err := openapicompat.CheckStableSDKSurfaceWithAllowedSchemaDemotions(base, revision, allowedSchemaDemotions); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
