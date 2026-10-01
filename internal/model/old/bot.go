package old

// ProviderRef is the minimal chat.bot row needed to resolve a
// public.bot_mapping.old_bot_id (flow_id) to the provider's own id --
// see BotStore.GetProviderIDsByFlowIDs.
type ProviderRef struct {
	ID     int `db:"id"`
	FlowID int `db:"flow_id"`
}
