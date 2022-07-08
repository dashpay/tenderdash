package types

// SetDashParams sets dash's some parameters to a block
// this method should call if we need to provide specific dash data
func (b *Block) SetDashParams(
	lastCoreChainLockedBlockHeight uint32,
	coreChainLock *CoreChainLock,
	proposedAppVersion uint64,
) {
	if coreChainLock == nil {
		b.CoreChainLockedHeight = lastCoreChainLockedBlockHeight
	} else {
		b.CoreChainLockedHeight = coreChainLock.CoreBlockHeight
	}
	b.ProposedAppVersion = proposedAppVersion
	b.CoreChainLock = coreChainLock
}
