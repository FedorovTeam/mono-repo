#!/bin/bash

echo "=== KUBERNETES NODE INFO ==="
hostname
kubectl get nodes -o wide
echo

echo "=== KUBELET SERVICE STATUS ==="
systemctl status kubelet --no-pager | head -20
echo

echo "=== KUBELET LOGS (last 100 lines) ==="
journalctl -u kubelet -n 100 --no-pager
echo

echo "=== SYSTEMD SERVICES (k3s, kubelet) ==="
systemctl list-units --type=service | grep -E 'k3s|kubelet'
echo

echo "=== DOCKER / CONTAINERD STATUS ==="
systemctl status docker containerd --no-pager | head -20
echo

echo "=== CHECKING KUBE-SYSTEM PODS ==="
kubectl get pods -n kube-system -o wide
echo

echo "=== CHECKING SERVICES ==="
kubectl get svc -A
echo

echo "=== KUBE-PROXY LOGS ==="
KUBE_PROXY_POD=$(kubectl get pods -n kube-system -l k8s-app=kube-proxy -o jsonpath="{.items[0].metadata.name}")
kubectl logs -n kube-system $KUBE_PROXY_POD | tail -n 50
echo

echo "=== DNS CHECK (coredns logs) ==="
COREDNS_POD=$(kubectl get pods -n kube-system -l k8s-app=kube-dns -o jsonpath="{.items[0].metadata.name}")
kubectl logs -n kube-system $COREDNS_POD | tail -n 50
