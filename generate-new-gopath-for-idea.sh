#!/usr/bin/env bash

GOPATH_FOR_IDEA=${PWD}/gopath-for-idea

rm -rf ${GOPATH_FOR_IDEA}
mkdir -p ${GOPATH_FOR_IDEA}/src

DEPS_WITHOUT_COREOS=`find ${PWD}/cmd/vendor -type d -maxdepth 2 -mindepth 2 -print | grep -v coreos`
for d in ${DEPS_WITHOUT_COREOS}; do
    name=`echo $d | rev | cut -d/ -f 1| rev`
    domain=`echo $d | rev | cut -d/ -f 2| rev`
    mkdir -p ${GOPATH_FOR_IDEA}/src/${domain}
    ln -s ${d} ${GOPATH_FOR_IDEA}/src/${domain}/${name}
done

DEPS_IN_COREOS=`find . ${PWD}/cmd/vendor/github.com/coreos -type d -mindepth 1 -maxdepth 1 | grep coreos`
for d in ${DEPS_IN_COREOS}; do
    name=`echo $d | rev | cut -d/ -f 1| rev`
    repo=`echo $d | rev | cut -d/ -f 2| rev`
    mkdir -p ${GOPATH_FOR_IDEA}/src/github.com/${repo}
    ln -s ${d} ${GOPATH_FOR_IDEA}/src/github.com/${repo}/${name}
done

DEPS_IN_ETCD=`find ${PWD} -type d -maxdepth 1 -mindepth 1 | grep -v -E '\.git|\.idea|gopath-for-idea|cmd|Documentation|bin|logo|tools|gopath'`
for d in ${DEPS_IN_ETCD}; do
    name=`echo $d | rev | cut -d/ -f 1| rev`
    mkdir -p ${GOPATH_FOR_IDEA}/src/github.com/coreos/etcd
    ln -s ${d} ${GOPATH_FOR_IDEA}/src/github.com/coreos/etcd/${name}
done