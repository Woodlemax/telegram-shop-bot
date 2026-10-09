import { defineRailway, project, service, volume } from "railway/iac";

export default defineRailway(() => {
  const shopBotVolume = volume("shop-bot-volume", {
    alerts: { usage: { "80": {}, "95": {}, "100": {} } },
    allowOnlineResize: true,
    region: "iad",
    sizeMB: 500,
  });
  const shopBot = service("shop-bot", {
    build: { buildEnvironment: "V3", builder: "DOCKERFILE", dockerfilePath: "Dockerfile" },
    healthcheck: "/health",
    healthcheckTimeout: 120,
    replicas: { "iad": 1 },
    deploy: { drainingSeconds: 20, overlapSeconds: 0 },
    volumeMounts: { "/app/data": shopBotVolume },
  });

  return project("telegram-shop-bot", {
    // Secrets stay in Railway Variables; this file never reads or writes them.
    variables: { managed: false },
    resources: [shopBot, shopBotVolume],
  });
});
