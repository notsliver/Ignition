// Package functions runs a single handler file as an HTTP service: no Dockerfile, no build. The
// official runtime image starts a small built-in server (shipped in IGNITION_FUNCTION_SERVER) that
// loads the handler from FUNCTION_CODE and turns each request into a call.
package functions

import (
	"errors"
	"strings"
)

const (
	Port        = 8080
	MaxCodeSize = 100 << 10 // fits comfortably in one environment variable
	CodeVar     = "FUNCTION_CODE"
	ServerVar   = "IGNITION_FUNCTION_SERVER"
)

type Runtime struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Image    string `json:"image"`
	File     string `json:"file"`
	Template string `json:"template"`
	server   string
	run      string
}

var Runtimes = []Runtime{
	{ID: "node", Label: "Node.js 22", Image: "node:22-alpine", File: "handler.mjs", run: "node server.mjs", server: nodeServer,
		Template: `// Called for every request. Return a Response, an object (sent as JSON), a string, or nothing (204).
export default async function handler(request, { env }) {
  const url = new URL(request.url);
  if (request.method === 'POST') {
    const body = await request.json().catch(() => ({}));
    return { received: body };
  }
  return { hello: url.searchParams.get('name') ?? 'world', at: new Date().toISOString() };
}
`},
	{ID: "python", Label: "Python 3.12", Image: "python:3.12-alpine", File: "handler.py", run: "python -u server.py", server: pythonServer,
		Template: `# Called for every request with a dict: method, path, query, headers, body (text) and json (if sent).
# Return a dict or list (sent as JSON), a string, None (204), or (body, status[, headers]).
import datetime

def handler(request):
    if request["method"] == "POST":
        return {"received": request.get("json")}
    name = request["query"].get("name", "world")
    return {"hello": name, "at": datetime.datetime.utcnow().isoformat() + "Z"}
`},
}

func RuntimeByID(id string) *Runtime {
	for i := range Runtimes {
		if Runtimes[i].ID == id {
			return &Runtimes[i]
		}
	}
	return nil
}

// Command is the start command: write the server and handler into /tmp (writable even with a
// read-only root) and run the server.
func (r *Runtime) Command() string {
	return `mkdir -p /tmp/fn && cd /tmp/fn && printf '%s' "$` + ServerVar + `" > ` + serverFile(r) + ` && printf '%s' "$` + CodeVar + `" > ` + r.File + ` && exec ` + r.run
}

func serverFile(r *Runtime) string {
	if r.ID == "python" {
		return "server.py"
	}
	return "server.mjs"
}

// Server is the built-in server's source for a runtime.
func (r *Runtime) Server() string { return r.server }

// Setup is what a function resource runs.
type Setup struct {
	Image, Command string
	Port           int
	Vars           [][2]string // non-secret variables: the built-in server, the handler and PORT
}

// Prepare returns the image, command, port and variables for a handler (the template if empty).
func Prepare(runtime, code string) (Setup, error) {
	rt := RuntimeByID(runtime)
	if rt == nil {
		return Setup{}, errors.New("choose Node.js or Python")
	}
	if strings.TrimSpace(code) == "" {
		code = rt.Template
	}
	if len(code) > MaxCodeSize {
		return Setup{}, errors.New("the handler is larger than 100 KB; deploy it as an application from Git instead")
	}
	return Setup{Image: rt.Image, Command: rt.Command(), Port: Port, Vars: [][2]string{{ServerVar, rt.server}, {CodeVar, code}, {"PORT", "8080"}}}, nil
}

const nodeServer = `import http from 'node:http';
const mod = await import('./handler.mjs').catch(e => { console.error('Your handler failed to load:', e); process.exit(1); });
const handler = mod.default ?? mod.handler;
if (typeof handler !== 'function') { console.error('handler.mjs must export default async function handler(request)'); process.exit(1); }
const port = Number(process.env.PORT || 8080);
http.createServer(async (req, res) => {
  const started = Date.now();
  let status = 500;
  try {
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const url = new URL(req.url, 'http://' + (req.headers.host || 'localhost'));
    const headers = new Headers();
    for (const [k, v] of Object.entries(req.headers)) headers.set(k, Array.isArray(v) ? v.join(', ') : v);
    const request = new Request(url, { method: req.method, headers, body: ['GET', 'HEAD'].includes(req.method) ? undefined : Buffer.concat(chunks) });
    let out = await handler(request, { env: process.env });
    if (!(out instanceof Response)) {
      out = out == null ? new Response(null, { status: 204 })
        : typeof out === 'string' ? new Response(out, { headers: { 'content-type': 'text/plain; charset=utf-8' } })
        : Response.json(out);
    }
    status = out.status;
    res.writeHead(out.status, Object.fromEntries(out.headers));
    res.end(Buffer.from(await out.arrayBuffer()));
  } catch (e) {
    console.error(req.method + ' ' + req.url + ' threw:', e);
    res.writeHead(500, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: 'Function error', message: String(e && e.message || e) }));
  }
  console.log(req.method + ' ' + req.url + ' ' + status + ' ' + (Date.now() - started) + 'ms');
}).listen(port, () => console.log('Function listening on :' + port));
`

const pythonServer = `import importlib.util, json, os, time, traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit, parse_qs

spec = importlib.util.spec_from_file_location("handler", "/tmp/fn/handler.py")
mod = importlib.util.module_from_spec(spec)
try:
    spec.loader.exec_module(mod)
except Exception:
    traceback.print_exc()
    raise SystemExit("Your handler failed to load")
handler = getattr(mod, "handler", None)
if not callable(handler):
    raise SystemExit("handler.py must define handler(request)")

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def respond(self):
        started = time.time()
        length = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(length) if length else b""
        u = urlsplit(self.path)
        request = {"method": self.command, "path": u.path, "query": {k: v[0] if len(v) == 1 else v for k, v in parse_qs(u.query).items()},
                   "headers": dict(self.headers), "body": raw.decode("utf-8", "replace"), "json": None}
        if raw and "json" in self.headers.get("content-type", ""):
            try:
                request["json"] = json.loads(raw)
            except ValueError:
                pass
        status, headers = 200, {}
        try:
            out = handler(request)
            if isinstance(out, tuple):
                parts = list(out)
                out = parts[0]
                status = parts[1] if len(parts) > 1 else 200
                headers = dict(parts[2]) if len(parts) > 2 else {}
            if out is None:
                data, status = b"", 204 if status == 200 else status
            elif isinstance(out, (dict, list)):
                data = json.dumps(out).encode()
                headers.setdefault("content-type", "application/json")
            elif isinstance(out, bytes):
                data = out
            else:
                data = str(out).encode()
                headers.setdefault("content-type", "text/plain; charset=utf-8")
        except Exception as e:
            traceback.print_exc()
            status, headers = 500, {"content-type": "application/json"}
            data = json.dumps({"error": "Function error", "message": str(e)}).encode()
        self.send_response(status)
        for k, v in headers.items():
            self.send_header(k, v)
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        print(f"{self.command} {self.path} {status} {int((time.time() - started) * 1000)}ms", flush=True)
    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = do_HEAD = respond
    def log_message(self, *args):
        pass

port = int(os.environ.get("PORT", "8080"))
print(f"Function listening on :{port}", flush=True)
ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
`
