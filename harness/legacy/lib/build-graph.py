#!/usr/bin/env python3
"""
build-graph.py - Build a code graph using graphify's Python API.

Usage:
  python3 build-graph.py <repo_path>

Outputs:
  - graphify-out/graph.json
  - graphify-out/GRAPH_REPORT.md
"""

import sys
import json
from pathlib import Path

# Import graphify modules
try:
    from graphify.detect import detect
    from graphify.extract import collect_files, extract
    from graphify.build import build_from_json
    from graphify.cluster import cluster, score_all
    from graphify.analyze import god_nodes, surprising_connections, suggest_questions
    from graphify.report import generate
    from graphify.export import to_json
except ImportError as e:
    print(f"Error: graphify module not found: {e}", file=sys.stderr)
    print("Install with: pip install graphifyy", file=sys.stderr)
    sys.exit(1)


def build_graph(repo_path):
    """Build a complete code graph for the given repository."""
    repo = Path(repo_path).resolve()

    if not repo.is_dir():
        print(f"Error: {repo} is not a directory", file=sys.stderr)
        sys.exit(1)

    output_dir = repo / "graphify-out"
    output_dir.mkdir(exist_ok=True)

    # Step 1: Detect files
    print("Detecting files...", file=sys.stderr)
    detection = detect(repo)

    # Step 2: Extract AST (code files only)
    print(f"Extracting AST from {detection.get('files', {}).get('code', []).__len__() if isinstance(detection.get('files', {}), dict) else 0} code files...", file=sys.stderr)
    code_files = []
    for f in detection.get('files', {}).get('code', []):
        file_path = Path(f) if Path(f).is_absolute() else repo / f
        if file_path.is_dir():
            code_files.extend(collect_files(file_path))
        else:
            code_files.append(file_path)

    if code_files:
        ast_result = extract(code_files, cache_root=repo)
        print(f"AST: {len(ast_result['nodes'])} nodes, {len(ast_result['edges'])} edges", file=sys.stderr)
    else:
        ast_result = {'nodes': [], 'edges': [], 'input_tokens': 0, 'output_tokens': 0}

    # Step 3: Merge (no semantic extraction for migration-harness - pure AST)
    semantic_result = {'nodes': [], 'edges': [], 'hyperedges': [], 'input_tokens': 0, 'output_tokens': 0}

    seen = {n['id'] for n in ast_result['nodes']}
    merged_nodes = list(ast_result['nodes'])
    for n in semantic_result['nodes']:
        if n['id'] not in seen:
            merged_nodes.append(n)
            seen.add(n['id'])

    merged = {
        'nodes': merged_nodes,
        'edges': ast_result['edges'] + semantic_result['edges'],
        'hyperedges': semantic_result.get('hyperedges', []),
        'input_tokens': 0,
        'output_tokens': 0,
    }

    # Step 4: Build graph and cluster
    print("Building graph and detecting communities...", file=sys.stderr)
    G = build_from_json(merged)
    communities = cluster(G)
    cohesion = score_all(G, communities)
    tokens = {'input': 0, 'output': 0}
    gods = god_nodes(G)
    surprises = surprising_connections(G, communities)

    # Generate labels
    labels = {cid: f'Community {cid}' for cid in communities}
    questions = suggest_questions(G, communities, labels)

    print(f"Graph: {G.number_of_nodes()} nodes, {G.number_of_edges()} edges, {len(communities)} communities", file=sys.stderr)

    # Step 5: Export
    report = generate(G, communities, cohesion, labels, gods, surprises, detection, tokens, str(repo), suggested_questions=questions)
    (output_dir / 'GRAPH_REPORT.md').write_text(report, encoding='utf-8')

    # Export graph to JSON
    to_json(G, communities, str(output_dir / 'graph.json'))

    # Add communities to the graph.json (to_json doesn't include them)
    graph_data = json.loads((output_dir / 'graph.json').read_text(encoding='utf-8'))
    graph_data['communities'] = [
        {'id': cid, 'nodes': node_list}
        for cid, node_list in communities.items()
    ]
    (output_dir / 'graph.json').write_text(json.dumps(graph_data, indent=2, ensure_ascii=False), encoding='utf-8')

    print(f"✓ Graph written to {output_dir}/graph.json", file=sys.stderr)
    print(f"✓ Report written to {output_dir}/GRAPH_REPORT.md", file=sys.stderr)


if __name__ == '__main__':
    if len(sys.argv) < 2:
        print('Usage: python3 build-graph.py <repo_path>', file=sys.stderr)
        sys.exit(1)

    build_graph(sys.argv[1])
