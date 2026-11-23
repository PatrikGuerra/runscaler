# runscaler com o Home Assistant

Este fork deixa o Home Assistant ver e mudar o runscaler. A integração do HA fica em outro repositório: `PatrikGuerra/ha-runscaler`.

## Como as partes se falam

```text
Home Assistant (network_mode: host)
  ├── GET  http://127.0.0.1:8089/healthz           → estado
  ├── lê   /runscaler-exchange/outbox/*.json       → config atual, resultado do pedido
  ├── lê   /runscaler-exchange/logs/runner.log     → log
  └── grava /runscaler-exchange/inbox/request.json → pedido
runscaler (este fork): internal/inbox valida o pedido → aplica → drain → reinicia
```

O `internal/inbox` aceita só uma lista fechada de campos. Um pedido não consegue ligar o `dind`, trocar a rede nem montar volumes. O contrato JSON está em `internal/inbox/testdata/contract/`.

## Instalar no host

1. Crie a pasta de troca. Os dois lados rodam como uid 0, então `root` é o dono:

   ```bash
   sudo install -d -o root -g root -m 0700 \
     /opt/runscaler/exchange /opt/runscaler/exchange/inbox \
     /opt/runscaler/exchange/outbox /opt/runscaler/exchange/logs
   ```

2. Em `/opt/runscaler/data/config.toml`, acrescente:

   ```toml
   log-file = "/exchange/logs/runner.log"
   health-address = "0.0.0.0"   # inside the container; compose publishes it only on 127.0.0.1
   health-port = 8089
   ```

3. Copie o `compose.yaml` desta pasta para `/opt/runscaler/compose.yaml` e suba:

   ```bash
   cd /opt/runscaler
   sudo docker compose pull
   sudo docker compose up -d
   curl -s http://127.0.0.1:8089/healthz
   ```

4. No compose do Home Assistant, acrescente o volume e recrie o container:

   ```yaml
       volumes:
         - /opt/runscaler/exchange:/runscaler-exchange
   ```

## Se uma config nova não subir

O `inbox` guarda a config anterior em `config.toml.bak`:

```bash
sudo cp /opt/runscaler/data/config.toml.bak /opt/runscaler/data/config.toml
sudo docker compose restart runscaler
```

## Imagem

O workflow `.github/workflows/images.yml` publica `ghcr.io/patrikguerra/runscaler:<tag>` (arm64 e amd64) quando uma tag `v*-ha.*` é enviada. O `Dockerfile` compila na máquina de build e termina em `scratch`, então o arm64 sai sem emulação.
