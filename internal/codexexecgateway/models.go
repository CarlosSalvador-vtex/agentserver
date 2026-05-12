// Package codexexecgateway: types are defined in execmodel to avoid an import
// cycle with the handlers sub-package. Type aliases here re-export them under
// the parent package name so existing code outside this package is unaffected.
package codexexecgateway

import "github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"

// Re-export aliases preserve the package's external API.
// *Executor and *execmodel.Executor are the same type — aliases are zero-cost.
type Executor = execmodel.Executor
type WorkspaceExecutor = execmodel.WorkspaceExecutor
type ConnectedExecutor = execmodel.ConnectedExecutor
