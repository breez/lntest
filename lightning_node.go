package lntest

type LightningNode interface {
	NodeId() []byte
	Host() *string
	Port() *uint32
	TearDown() error
}
