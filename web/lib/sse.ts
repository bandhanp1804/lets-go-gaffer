// A server-sent-events parser that takes text as it arrives, in pieces of any size.

export interface SSEEvent {
  event: string;
  data: string;
}

/** Returns a function to feed decoded text into; onEvent runs once per complete event. */
export function createSSEParser(onEvent: (e: SSEEvent) => void): (chunk: string) => void {
  let buffer = "";
  return (chunk: string) => {
    buffer += chunk.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    for (;;) {
      const end = buffer.indexOf("\n\n");
      if (end < 0) return;
      const block = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);
      let event = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line === "" || line.startsWith(":")) continue; // comment or keep-alive
        const colon = line.indexOf(":");
        const field = colon < 0 ? line : line.slice(0, colon);
        let value = colon < 0 ? "" : line.slice(colon + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        if (field === "event") event = value;
        else if (field === "data") data.push(value);
      }
      if (data.length > 0) onEvent({ event, data: data.join("\n") });
    }
  };
}
