#!/bin/sh
# Runs from nginx's own /docker-entrypoint.d/ before the server starts.
#
# The image is built once and pointed at a gateway at run time, so the two
# things that vary per deployment — where the API is, and whose name decisions
# are filed under — are written here rather than baked into the bundle.
set -eu

html_escape() {
  # These values land inside a JS string literal in a served file. Refuse the
  # characters that could end the literal or the script element rather than
  # trying to escape them.
  case "$1" in
    *\'* | *\"* | *\\* | *'<'* | *'>'*)
      echo "mcp-shield-admin: refusing unsafe character in configuration value" >&2
      exit 1
      ;;
  esac
  printf '%s' "$1"
}

API_BASE_URL_SAFE=$(html_escape "${API_BASE_URL:-}")
OPERATOR_NAME_SAFE=$(html_escape "${OPERATOR_NAME:-}")

cat > /usr/share/nginx/html/config.js <<EOF
window.__MCP_SHIELD__ = {
  apiBaseUrl: '${API_BASE_URL_SAFE}',
  operator: '${OPERATOR_NAME_SAFE}',
};
EOF

# Same-origin API. Preferred over API_BASE_URL: it keeps the browser on one
# origin, so the gateway never needs CORS headers and no credential is sent
# cross-site.
if [ -n "${API_PROXY_PASS:-}" ]; then
  cat > /etc/nginx/conf.d/api-proxy.inc <<EOF
location /api/ {
    proxy_pass ${API_PROXY_PASS};
    proxy_set_header Host \$host;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
}
location = /healthz {
    proxy_pass ${API_PROXY_PASS};
    proxy_set_header Host \$host;
}
EOF
else
  : > /etc/nginx/conf.d/api-proxy.inc
fi

echo "mcp-shield-admin: apiBaseUrl='${API_BASE_URL_SAFE}' proxy='${API_PROXY_PASS:-none}'"
