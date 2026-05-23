#!/usr/bin/env python3
"""Convert projectlens graph JSON export to GraphML for Gephi."""

import argparse
import json
import sys
from pathlib import Path

try:
    import networkx as nx
except ImportError:
    sys.exit("networkx not installed. Run: pip install networkx")


def flatten(prefix: str, value, out: dict) -> None:
    if isinstance(value, dict):
        for k, v in value.items():
            flatten(f"{prefix}_{k}" if prefix else k, v, out)
    elif isinstance(value, list):
        out[prefix] = ",".join(str(x) for x in value)
    elif value is None:
        return
    else:
        out[prefix] = value


def build_graph(data: dict, edge_types: set[str] | None, drop_isolated: bool) -> nx.DiGraph:
    G = nx.DiGraph()
    for n in data["nodes"]:
        attrs: dict = {"label": n.get("label", ""), "type": n.get("type", "")}
        flatten("attr", n.get("attrs", {}), attrs)
        G.add_node(n["id"], **{k: v for k, v in attrs.items() if v is not None})

    kept = 0
    for e in data["edges"]:
        if edge_types and e.get("type") not in edge_types:
            continue
        src, tgt = e["source"], e["target"]
        if src not in G or tgt not in G:
            continue
        attrs: dict = {"type": e.get("type", "")}
        flatten("prop", e.get("properties", {}), attrs)
        G.add_edge(src, tgt, **{k: v for k, v in attrs.items() if v is not None})
        kept += 1

    if drop_isolated:
        isolates = [n for n in G.nodes if G.degree(n) == 0]
        G.remove_nodes_from(isolates)

    print(f"nodes: {G.number_of_nodes()}  edges: {G.number_of_edges()}  (kept {kept})", file=sys.stderr)
    return G


def main() -> None:
    ap = argparse.ArgumentParser(description="Convert projectlens graph JSON to GraphML/GEXF for Gephi.")
    ap.add_argument("input", type=Path, help="projectlens-graph.json")
    ap.add_argument("-o", "--output", type=Path, default=Path("projectlens.graphml"))
    ap.add_argument("-f", "--format", choices=["graphml", "gexf"], default="graphml")
    ap.add_argument("-e", "--edges", help="Comma list of edge types to keep (default: all). e.g. calls,implements")
    ap.add_argument("--keep-isolated", action="store_true", help="Keep nodes with no edges after filtering.")
    args = ap.parse_args()

    data = json.loads(args.input.read_text())
    edge_types = set(args.edges.split(",")) if args.edges else None
    G = build_graph(data, edge_types, drop_isolated=not args.keep_isolated)

    if args.format == "graphml":
        nx.write_graphml(G, args.output)
    else:
        nx.write_gexf(G, args.output)

    print(f"wrote {args.output}", file=sys.stderr)


if __name__ == "__main__":
    main()
