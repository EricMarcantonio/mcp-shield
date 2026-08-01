// Runtime configuration, read before the application bundle.
//
// In the Docker image this file is regenerated at container start from
// $API_BASE_URL (see docker-entrypoint.sh), so one built image can be pointed
// at any gateway. Leaving apiBaseUrl empty means "same origin", which is what
// the Vite dev proxy and a reverse-proxied deployment both want.
window.__MCP_SHIELD__ = {
  apiBaseUrl: '',
  operator: '',
};
