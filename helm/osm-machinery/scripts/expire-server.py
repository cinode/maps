#!/usr/bin/env python3

import http.server
import socketserver
import subprocess

PORT = 8642


class CustomHTTPRequestHandler(http.server.SimpleHTTPRequestHandler):
    def do_POST(self):
        try:
            # Get content length
            content_length = int(self.headers.get("Content-Length", 0))

            # Read the request body
            body = self.rfile.read(content_length)

            # Execute command with received file
            out = subprocess.check_output(
                [
                    "render_expired",
                    "--map=default",
                    "--min-zoom=4",
                    "--max-zoom=20",
                    "--delete-from=4",
                    "-s",
                    "/run/renderd/renderd.sock",
                ],
                input=body,
            )

            # Send response
            self.send_response(200)
            self.end_headers()
            self.wfile.write(out)

        except Exception as e:
            error_msg = f"Error processing request: {str(e)}\n"
            self.send_response(500)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", len(error_msg))
            self.end_headers()
            self.wfile.write(error_msg.encode())


def main():
    with socketserver.ThreadingTCPServer(
        ("0.0.0.0", PORT),
        CustomHTTPRequestHandler,
    ) as server:
        print(f"HTTP server listening on 0.0.0.0:{ PORT }")
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            print("\nShutting down server")
            server.server_close()


if __name__ == "__main__":
    main()
