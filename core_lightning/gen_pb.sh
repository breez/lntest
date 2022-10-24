SCRIPTDIR=$(dirname $0)
PROTO_BUILD=$SCRIPTDIR/build
PROTO_ROOT=$SCRIPTDIR/proto
GITHUB_PKG="http://github.com/breez/lntest/core_lightning"

rm -rf $PROTO_BUILD/*
mkdir $PROTO_BUILD
cp -r $PROTO_ROOT/* $PROTO_BUILD/
# TODO: Automatically add 'option go_package' to the proto files (below doesn't work)
#find $PROTO_BUILD -name '*.proto' -exec sed -r -i'' -e "/package cln;/a option go_package = \"$GITHUB_PKG\";" {} \;

protoc --go_out=$SCRIPTDIR --go_opt=paths=source_relative --go-grpc_out=$SCRIPTDIR --go-grpc_opt=paths=source_relative -I=$PROTO_BUILD $PROTO_BUILD/* 