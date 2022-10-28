package lntest

type LightningNode interface {
	NodeId() []byte
	TearDown() error
}
