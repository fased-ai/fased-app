package onchain

// ControlledBlockhash is a deliberately inert fixture identity. The existing
// fixture signer requires this hash; it never signs a public-chain blockhash.
// Production managed-wallet recovery/admission remains closed.
const ControlledBlockhash = "11111111111111111111111111111112"
