module github.com/breez/lntest

go 1.18

require (
	github.com/niftynei/glightning v0.8.2
	golang.org/x/exp v0.0.0-20221012211006-4de253d81b95
	google.golang.org/grpc v1.50.1
	google.golang.org/protobuf v1.27.1
)

require go.uber.org/atomic v1.7.0 // indirect

require (
	github.com/golang/protobuf v1.5.2 // indirect
	go.uber.org/multierr v1.8.0
	golang.org/x/net v0.0.0-20201021035429-f5854403a974 // indirect
	golang.org/x/sys v0.0.0-20220722155257-8c9f86f7a55f // indirect
	golang.org/x/text v0.3.3 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
)


replace github.com/niftynei/glightning v0.8.2 => github.com/breez/glightning v0.0.0-20221201090905-688d312b61f4