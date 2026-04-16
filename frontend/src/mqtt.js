/** MQTT-over-WebSocket client for Fernsicht. */
import mqtt from "mqtt";
import { buildTopic } from "./protocol";
const BROKER_URL = "wss://broker.emqx.io:8084/mqtt";
export function connect(topicId, onMessage, onStatus) {
    const topic = buildTopic(topicId);
    const clientId = `fernsicht-sub-${Math.random().toString(36).slice(2, 10)}`;
    onStatus("connecting");
    const client = mqtt.connect(BROKER_URL, {
        clientId,
        clean: true,
        reconnectPeriod: 3000,
    });
    client.on("connect", () => {
        onStatus("connected");
        client.subscribe(topic, { qos: 1 });
    });
    client.on("close", () => {
        onStatus("disconnected");
    });
    client.on("message", (_topic, payload) => {
        onMessage(new TextDecoder().decode(payload));
    });
    return {
        disconnect() {
            client.end();
        },
    };
}
