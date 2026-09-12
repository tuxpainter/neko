---
description: Running Neko locally for development
slug: /developer-guide/development
---

# Local Development

The fastest way to contribute to Neko is to run the backend in Docker and the frontend locally with hot reload. No need to rebuild the whole Docker image on every change.

The only prerequisite is [Docker](https://docs.docker.com/get-docker/).

Start by cloning the repository:

```bash
git clone https://github.com/m1k1o/neko.git
cd neko
```

## Backend {#backend}

All backend dev scripts live in `server/dev/`.

### First-time setup

Build the required Docker images (only needed once, or after major dependency changes):

```bash
cd server/dev
./build
```

### Starting the server

```bash
cd server/dev
./start
```

This starts the neko backend inside Docker and exposes it on port **3000**. The container is named `neko_server_dev` and is kept running in the foreground.

To test WebTransport, start the backend with local TLS enabled:

```bash
cd server/dev
NEKO_TLS=1 ./start
```

The script uses OpenSSL to create a short-lived ECDSA localhost certificate under the ignored `server/dev/runtime/certs/` directory. It writes the certificate hash and an IPv4 WebTransport host override to the ignored `client/.env` file so local frontend builds can authenticate WebTransport without Web PKI. Import `server/dev/runtime/certs/neko-dev-ca.pem` as a trusted certificate authority, or open `https://localhost:3000` once and accept the certificate warning, so HTTPS and WebSocket connections are also allowed. Backend port 3000 is exposed over TCP for HTTPS and WebSocket traffic and UDP for HTTP/3 and WebTransport.

You can pass `nvidia` or `intel` as an argument to enable GPU acceleration:

```bash
./start nvidia
./start intel
```

### Applying backend changes (live rebuild)

After editing Go source files, rebuild and hot-swap the binary into the running container **without restarting Docker**:

```bash
# in a new terminal
cd server/dev
./rebuild
```

`./rebuild` compiles the server, copies the new binary (and any plugins) into the running `neko_server_dev` container, then tells supervisord to restart only the neko process - the full Docker image is never rebuilt.

## Frontend {#frontend}

All frontend dev scripts live in `client/dev/`.

### Installing dependencies

Dependencies are installed automatically the first time you run `./serve`. To install them manually (or to force a reinstall), run:

```bash
cd client/dev
./serve -i
```

Alternatively, use the provided npm wrapper that runs inside Docker:

```bash
cd client/dev
./npm install
```

### Starting the dev server with hot reload

```bash
cd client/dev
./serve
```

This starts the Vue dev server on port **3001**, proxying API calls to the backend on port **3000**. Any change you save to a file under `client/src/` is reflected in the browser instantly - no page reload required.

When the backend uses local TLS, start the frontend with secure backend connections and HTTPS hosting:

```bash
cd client/dev
VUE_APP_SERVER_TLS=true ./serve
```

Open `https://localhost:3001/?media=webcodecs-wt` to use WebTransport media. The Vue development server reuses the generated localhost certificate so the page is a secure context. WebSocket, API, and WebTransport connections go directly to the TLS-enabled backend on port 3000.

Restart the frontend after regenerating the backend certificate so Vue reloads the certificate hash from `client/.env`.

| Service | URL |
|---------|-----|
| Backend (Docker) | `http://localhost:3000` |
| Frontend (hot reload) | `http://localhost:3001`, or `https://localhost:3001` with TLS |

## Typical workflow

1. **Terminal 1** - start the backend: `cd server/dev && ./start`
2. **Terminal 2** - start the frontend: `cd client/dev && ./serve`
3. Open `http://localhost:3001` in your browser.
4. Edit frontend files → browser updates automatically.
5. Edit backend files → run `cd server/dev && ./rebuild` in **Terminal 3** to apply changes.
