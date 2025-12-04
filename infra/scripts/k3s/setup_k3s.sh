#!/bin/bash
# Универсальный скрипт для установки K3s на сервер (используется в null_resource или вручную)
set -e
K3S_VERSION=${1:-v1.29.3+k3s1}
NODE_ROLE=${2:-server}
export INSTALL_K3S_VERSION=$K3S_VERSION

curl -sfL https://get.k3s.io | sh -s - --${NODE_ROLE} --disable traefik

# После установки K3s
mkdir -p $HOME/.kube
sudo cp /etc/rancher/k3s/k3s.yaml $HOME/.kube/config-k3s
sudo chown $(id -u):$(id -g) $HOME/.kube/config-k3s

kubectl --kubeconfig=$HOME/.kube/config-k3s get nodes

sudo cp /etc/rancher/k3s/k3s.yaml $HOME/.kube/config-k3s
sudo chown $(id -u):$(id -g) $HOME/.kube/config-k3s

kubectl --kubeconfig=$HOME/.kube/config-k3s get nodes
