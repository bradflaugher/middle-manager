import sys
import json
import asyncio

async def main():
    loop = asyncio.get_event_loop()
    reader = asyncio.StreamReader()
    protocol = asyncio.StreamReaderProtocol(reader)
    await loop.connect_read_pipe(lambda: protocol, sys.stdin)
    
    session_cwd = None
    is_first_prompt = True
    
    while True:
        line = await reader.readline()
        if not line:
            break
        try:
            req = json.loads(line.decode('utf-8'))
        except Exception:
            continue
            
        req_id = req.get("id")
        method = req.get("method")
        params = req.get("params", {})
        
        if method == "initialize":
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "protocolVersion": 1,
                    "capabilities": {},
                    "serverInfo": {
                        "name": "agy-acp",
                        "version": "1.0.0"
                    }
                }
            }
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()
        elif method == "session/new":
            session_cwd = params.get("cwd")
            is_first_prompt = True
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "sessionId": "session_1"
                }
            }
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()
        elif method == "session/prompt":
            prompt_list = params.get("prompt", [])
            prompt_text = ""
            for p in prompt_list:
                if p.get("type") == "text":
                    prompt_text += p.get("text", "")
            
            # Spawn agy
            cmd = ["agy", "--dangerously-skip-permissions"]
            if session_cwd:
                cmd.extend(["--add-dir", session_cwd])
            if not is_first_prompt:
                cmd.append("--continue")
            cmd.extend(["--print", prompt_text])
            
            is_first_prompt = False
            
            proc = await asyncio.create_subprocess_exec(
                cmd[0], *cmd[1:],
                stdin=asyncio.subprocess.DEVNULL,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )
            
            full_out = []
            
            async def read_stream(stream):
                while True:
                    chunk = await stream.read(4096)
                    if not chunk:
                        break
                    chunk_str = chunk.decode('utf-8', errors='ignore')
                    full_out.append(chunk_str)
                    # Send update notification
                    notif = {
                        "jsonrpc": "2.0",
                        "method": "session/update",
                        "params": {
                            "sessionId": "session_1",
                            "update": {
                                "type": "agent_message_chunk",
                                "content": {
                                    "type": "text",
                                    "text": chunk_str
                                }
                            }
                        }
                    }
                    sys.stdout.write(json.dumps(notif) + "\n")
                    sys.stdout.flush()
            
            # Read stdout and stderr concurrently
            await asyncio.gather(
                read_stream(proc.stdout),
                read_stream(proc.stderr)
            )
            
            await proc.wait()
            
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [
                        {
                            "type": "text",
                            "text": "".join(full_out)
                        }
                    ],
                    "stopReason": "end_turn"
                }
            }
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()

if __name__ == "__main__":
    asyncio.run(main())
