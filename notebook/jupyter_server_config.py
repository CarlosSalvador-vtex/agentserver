"""Plan 3b: plug AgentserverIdentityProvider + AgentserverKernelProvisioner.
base_url reads from NOTEBOOK_BASE_URL env (set per-workspace by the
supervisor) so generated jupyter URLs include /api/notebooks/{ws}/.
"""
import os
import sys

# Make the agentserver jupyter extension modules importable.
sys.path.insert(0, "/etc/jupyter/agentserver")

c = get_config()  # type: ignore[name-defined]  # noqa: F821 (provided by jupyter at runtime)

c.ServerApp.ip = "0.0.0.0"
c.ServerApp.port = 8888
c.ServerApp.open_browser = False
c.ServerApp.disable_check_xsrf = True
c.ServerApp.allow_origin = "*"
c.ServerApp.root_dir = "/workspace"
c.ServerApp.allow_root = True
c.ServerApp.base_url = os.environ.get("NOTEBOOK_BASE_URL", "/")

# Auth — trust X-Forwarded-User from agentserver web proxy.
c.ServerApp.identity_provider_class = "identity_provider.AgentserverIdentityProvider"

# Kernel provisioner — inject per-request user_id env.
#
# NOTE (Plan 3b smoke test, 2026-05-18): jupyter_client 8.x discovers
# provisioners via the "jupyter_client.kernel_provisioners" entry-point
# group, not via a traitlets config option. Setting
# `MultiKernelManager.kernel_provisioner_class` produces a warning and
# is silently ignored. To wire AgentserverKernelProvisioner properly we
# need to publish it as an entry-point in a tiny installable package
# (deferred until after Plan 1's SDK image lands so we can piggy-back
# on its packaging). The IdentityProvider above is sufficient for HTTP
# auth in v1; kernels will spawn without AGENTSERVER_USER_ID until the
# entry-point is registered.
