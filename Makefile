
.PHONY: mpx-cli mpx-ser mpx-tunnel
mpx-cli:
	go build -o mpx-cli ./mpx-tunnel/client 

mpx-ser:
	go build -o mpx-ser ./mpx-tunnel/server

mpx-tunnel:
	go build -o mpx ./mpx-tunnel