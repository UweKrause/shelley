import React, { useState } from "react";
import { LLMContent } from "../types";

interface WebSearchToolProps {
  toolInput?: unknown;
  isRunning?: boolean;
  searchResults?: LLMContent[];
  toolResult?: LLMContent[];
  hasError?: boolean;
  executionTime?: string;
}

function WebSearchResultItem({ result }: { result: LLMContent }) {
  const title = result.Title || "Untitled";
  const url = result.URL || "";
  const pageAge = result.PageAge || "";

  return (
    <div className="web-search-result">
      <a href={url} target="_blank" rel="noopener noreferrer" className="web-search-result-title">
        {title}
      </a>
      <div className="web-search-result-meta">
        <span className="web-search-result-url">{url}</span>
        {pageAge && <span className="web-search-result-age">{pageAge}</span>}
      </div>
    </div>
  );
}

function WebSearchTool({ toolInput, isRunning, searchResults, toolResult }: WebSearchToolProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  // Anthropic sends {"query": "..."}; OpenAI Responses sends {"queries": [...]}
  let queries: string[] = [];
  if (toolInput && typeof toolInput === "object") {
    const ti = toolInput as { query?: string; queries?: string[] };
    if (typeof ti.query === "string") queries = [ti.query];
    else if (Array.isArray(ti.queries)) queries = ti.queries;
  }
  const query = queries.join(" / ");

  const results = searchResults || toolResult || [];
  // OpenAI's server-side search doesn't deliver structured results to us;
  // the citations are attached to the assistant's message text instead.
  // So "complete with 0 results" is normal for OpenAI — only mark running
  // based on the isRunning flag.
  const isComplete = !isRunning;
  const resultCount = results.length;
  const showCount = resultCount > 0;

  return (
    <div className="tool" data-testid={isComplete ? "tool-call-completed" : "tool-call-running"}>
      <div className="tool-header" onClick={() => setIsExpanded(!isExpanded)}>
        <div className="tool-summary">
          <span className={`tool-emoji ${isRunning ? "running" : ""}`}>🔍</span>
          <span className="tool-command">
            Web Search{query ? ": " : ""}
            {query && <span className="web-search-query">{query}</span>}
          </span>
          {isComplete && showCount && (
            <span className="tool-success">
              {resultCount} result{resultCount !== 1 ? "s" : ""}
            </span>
          )}
        </div>
        <button
          className="tool-toggle"
          aria-label={isExpanded ? "Collapse" : "Expand"}
          aria-expanded={isExpanded}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 12 12"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            style={{
              transform: isExpanded ? "rotate(90deg)" : "rotate(0deg)",
              transition: "transform 0.2s",
            }}
          >
            <path
              d="M4.5 3L7.5 6L4.5 9"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
      </div>
      {isExpanded && results.length > 0 && (
        <div className="web-search-results">
          {results.map((result, index) => (
            <WebSearchResultItem key={index} result={result} />
          ))}
        </div>
      )}
    </div>
  );
}

export default WebSearchTool;
