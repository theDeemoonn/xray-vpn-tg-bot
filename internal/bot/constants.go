package bot

// --- Callback actions ---
const (
	// Prefix for plan selection
	callbackActionSelectPlan = "plan:"
	// Prefix for getting config
	callbackActionGetConfig = "config:"
	// Prefix for server selection
	callbackActionSelectServer = "servers:"
	// Prefix for confirming server configuration
	callbackActionConfigureServer = "configure_server:"
	// Action for FAQ category selection
	callbackActionFAQCategory = "faq_cat:"
	// Action for FAQ question selection
	callbackActionFAQQuestion = "faq_q:"
	// Action for instruction platform selection
	callbackActionInstructionPlatform = "instr_plat:"
	// Action for instruction details
	callbackActionInstructionDetails = "instr_det:"
	// Action for back to FAQ
	callbackActionBackToFAQ = "back_faq"
	// Action for back to instructions
	callbackActionBackToInstructions = "back_instr"
	// Action for back to help root
	callbackActionBackToHelpRoot = "back_help"
)

// --- Menu buttons ---
// These constants are exported to be used in handlers and keyboards
const (
	// Main menu buttons
	MainMenuButtonMySubscriptions = "🚀 Мои подписки"
	MainMenuButtonBuySubscription = "🛒 Купить подписку"
	MainMenuButtonReferral        = "🎁 Реф. программа"
	MainMenuButtonInstructions    = "📱 Инструкции"
	MainMenuButtonFAQ             = "❓ FAQ"
	MainMenuButtonSupport         = "💬 Поддержка"
	MainMenuButtonAdmin           = "⚙️ Панель администратора"
)
