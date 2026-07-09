/**
 * Local TCP listener that tunnels connections to a pod port via the
 * Kubernetes port-forward subresource — the programmatic equivalent of
 * `kubectl port-forward <pod> :4000`.
 *
 * Used when the client machine has no route to cluster service DNS (the
 * normal laptop-to-minikube case). Inside the cluster, or behind Hub's
 * stream proxy, connect to the service host directly instead.
 */
import * as net from "node:net";
import { inspect } from "node:util";
import * as k8s from "@kubernetes/client-node";

export interface Tunnel {
  localPort: number;
  close(): void;
}

export async function openTunnel(
  kc: k8s.KubeConfig,
  namespace: string,
  podName: string,
  targetPort: number,
): Promise<Tunnel> {
  const forward = new k8s.PortForward(kc);
  const server = net.createServer((socket) => {
    // Without a listener, a socket 'error' (destroy(err) below, or a
    // mid-stream failure surfaced by the port-forward piping) is an
    // unhandled 'error' event and kills the whole process.
    socket.on("error", (err) => {
      console.error(`[portforward] ${podName}:${targetPort} socket error: ${err.message}`);
    });
    forward
      .portForward(namespace, podName, [targetPort], socket, null, socket)
      .then((wsOrFactory) => {
        // With retryCount=0 (our call) this resolves with the API-server
        // WebSocket carrying the forward. The library pipes pod→local data
        // but never propagates upstream teardown: when the pod-side TCP
        // connection dies, the kubelet closes this WebSocket and the local
        // socket would sit open forever — whoever dialed through the tunnel
        // keeps waiting on a dead peer. Mirror the WS lifecycle onto the
        // local socket, and release the WS when the local socket goes first.
        if (typeof wsOrFactory === "function") return;
        const ws = wsOrFactory;
        ws.on("close", (code: number) => {
          if (!socket.destroyed) {
            socket.destroy(
              new Error(`port-forward to ${podName}:${targetPort} closed by upstream (code=${code})`),
            );
          }
        });
        ws.on("error", (err: Error) => {
          if (!socket.destroyed) {
            socket.destroy(
              new Error(`port-forward to ${podName}:${targetPort} errored: ${err.message}`),
            );
          }
        });
        socket.on("close", () => ws.close());
      })
      .catch((err) => {
        // Rejections are often ws ErrorEvents, not Errors — grab .message
        // before falling back to a full inspect dump.
        const reason =
          typeof (err as { message?: unknown })?.message === "string"
            ? (err as { message: string }).message
            : inspect(err, { depth: 2 });
        socket.destroy(new Error(`port-forward to ${podName}:${targetPort} failed: ${reason}`));
      });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  const address = server.address() as net.AddressInfo;
  return {
    localPort: address.port,
    close: () => server.close(),
  };
}
