#!/bin/bash
# Data Collection Profiler — startup script.
#
# Runs as the container's main process (PID 1) via docker-compose `command`.
# The wb CLI is installed and authenticated separately by the Workbench
# post-startup hook (postCreateCommand); this server calls `wb` lazily per
# request, so it can start before that completes.

echo "Starting dc-metadata-autofill..."

# Resolve the GCP project for Vertex AI (Gemini) from the metadata server if not
# already provided. This is the workspace's backing project (the pet SA's
# project), used as the billing/quota project for Vertex AI.
if [ -z "${GOOGLE_CLOUD_PROJECT}" ]; then
    GOOGLE_CLOUD_PROJECT=$(curl -s -H "Metadata-Flavor: Google" \
        "http://metadata.google.internal/computeMetadata/v1/project/project-id" 2>/dev/null || echo "")
fi
export GOOGLE_CLOUD_PROJECT

# Vertex AI location. gemini-2.5-flash is served from the "global" endpoint.
export GOOGLE_CLOUD_LOCATION="${GOOGLE_CLOUD_LOCATION:-global}"

echo "   GCP project:   ${GOOGLE_CLOUD_PROJECT:-<not set>}"
echo "   Vertex region: ${GOOGLE_CLOUD_LOCATION}"
echo "   Port:          ${PORT:-8080}"

exec /app/server
