#!/usr/bin/env bash

export GOPATH=~/GitHub/etcd-3.2.9/gopath/

go build main.go kvstore.go listener.go raft.go httpapi.go