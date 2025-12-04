echo "===== K8s summary ====="
kubectl get nodes -o wide
echo
for n in $(kubectl get nodes -o name | cut -d/ -f2); do
  echo "== $n =="
  kubectl describe node $n | sed -n '/Capacity/,+8p;/Allocatable/,+8p'
  echo
done

echo "== Metrics (top) =="
kubectl top nodes
kubectl top pods -A --sort-by=memory | head -n 20
kubectl top pods -A --sort-by=cpu    | head -n 20

echo "== Events (recent) =="
kubectl get events -A --sort-by=.metadata.creationTimestamp | tail -n 100 | egrep -i 'NotReady|InvalidDiskCapacity|Evict|OOM|BackOff|ImagePull|taint|eviction|disk|memory' || true
