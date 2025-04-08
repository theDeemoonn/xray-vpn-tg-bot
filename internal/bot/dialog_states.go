package bot

// Constants for admin server adding dialog states
const (
	serverAddStatePrefix         = "admin_server_add_"
	serverAddStateWaitName       = serverAddStatePrefix + "wait_name"
	serverAddStateWaitApiHost    = serverAddStatePrefix + "wait_api_host"
	serverAddStateWaitPublicHost = serverAddStatePrefix + "wait_public_host"
	serverAddStateWaitUsername   = serverAddStatePrefix + "wait_username"
	serverAddStateWaitPassword   = serverAddStatePrefix + "wait_password"
	serverAddStateWaitLocation   = serverAddStatePrefix + "wait_location"
	serverAddStateWaitInbound    = serverAddStatePrefix + "wait_inbound"
	serverAddStateConfirmation   = serverAddStatePrefix + "confirmation"
)

// Default inbound ID if not specified
const serverAddDefaultInbound = 1
