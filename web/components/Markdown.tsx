import type { ReactNode } from "react";
import { parseMarkdown, type Inline } from "../lib/markdown.ts";

function inline(parts: Inline[]): ReactNode[] {
  return parts.map((p, i) => {
    switch (p.kind) {
      case "bold":
        return <strong key={i}>{p.text}</strong>;
      case "code":
        return <code key={i}>{p.text}</code>;
      case "cite":
        return (
          <sup key={i} className="cite">
            <a href={`#source-${p.ref}`}>[{p.ref}]</a>
          </sup>
        );
      default:
        return <span key={i}>{p.text}</span>;
    }
  });
}

/** Renders an answer. Everything is built from text nodes: nothing the model wrote becomes markup. */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      {parseMarkdown(text).map((b, i) => {
        switch (b.kind) {
          case "h":
            return <h4 key={i}>{inline(b.inline)}</h4>;
          case "ul":
            return (
              <ul key={i}>
                {b.items.map((it, j) => (
                  <li key={j}>{inline(it)}</li>
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={i}>
                {b.items.map((it, j) => (
                  <li key={j}>{inline(it)}</li>
                ))}
              </ol>
            );
          case "table":
            return (
              <div className="tablewrap" key={i}>
                <table>
                  <thead>
                    <tr>
                      {b.head.map((c, j) => (
                        <th key={j}>{inline(c)}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {b.rows.map((r, j) => (
                      <tr key={j}>
                        {r.map((c, k) => (
                          <td key={k}>{inline(c)}</td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          default:
            return <p key={i}>{inline(b.inline)}</p>;
        }
      })}
    </div>
  );
}
