/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command kubectl-kai is the kai CLI packaged as a kubectl plugin. Named
// kubectl-kai, it is discovered on PATH so `kubectl kai ...` invokes it, giving
// operators an interactive way to manage this project's agentic CRDs (Gateways,
// Agents, Workflows and Skills) against the cluster in their KUBECONFIG.
//
// Install it by putting the built binary on PATH:
//
//	go install github.com/konveyor/agentic-controller/cmd/kubectl-kai@latest
//	kubectl kai gateway create
//
// The command tree lives in the reusable pkg/kai package, so other tools can
// embed the same commands as a subcommand of their own root.
package main

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"

	"github.com/konveyor/agentic-controller/pkg/kai"
)

func main() {
	// kai surfaces everything the operator needs on stdout/stderr directly;
	// the injected logger is for callers that want structured logs, so the
	// standalone plugin discards it.
	cmd := kai.NewKaiCommand(logr.Discard())
	// kai reports its own errors with the detail an operator needs; a usage
	// dump on failure would bury it.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if err := cmd.Execute(); err != nil {
		// SilenceErrors suppresses cobra's own printing, so surface the error
		// here before exiting non-zero.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
