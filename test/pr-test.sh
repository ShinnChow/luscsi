#! /usr/bin/env bash

set -x
set -e

source /etc/profile || true
export GOVC_INSECURE=1
export GOVC_RESOURCE_POOL="fupan-k8s"
export hosts="fu-rocky9.4-ci-10.6.113.61"
export snapshot="K8S"


for h in $hosts; do
  if [[ `govc vm.info $h | grep poweredOn | wc -l` -eq 1 ]]; then
    govc vm.power -off -force $h
    echo -e "\033[35m === $h has been down === \033[0m"
  fi

  govc snapshot.revert -vm $h $snapshot
  echo -e "\033[35m === $h reverted to snapshot: `govc snapshot.tree -vm $h -C -D -i -d` === \033[0m"

  govc vm.power -on $h
  echo -e "\033[35m === $h: power turned on === \033[0m"
done

yq -i '.global.k8sImageRegistry = "k8s.m.daocloud.io"' ./deploy/luscsi/values.yaml
yq -i '.global.luscsiImageRegistry = "10.6.112.210"' ./deploy/luscsi/values.yaml
yq -i ".luscsiNode.luscsi.image.tag = \"${GITHUB_RUN_ID}\"" ./deploy/luscsi/values.yaml



ginkgo -timeout=10h --fail-fast  --label-filter=${E2E_TESTING_LEVEL} test/e2e

