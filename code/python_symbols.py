"""Extract symbols from Python source files using the ast module.

Called by PythonCodeServer via subprocess. Outputs JSON to stdout.

Usage:
    python python_symbols.py <file_or_directory>

If given a file, outputs symbols for that file.
If given a directory, outputs symbols for all .py files in it.

Extracts: functions, async functions, classes, methods, variables,
type-annotated assignments, decorators, class attributes, imports,
__all__ exports, base classes (inheritance).
"""

import ast
import json
import os
import sys


def extract_symbols(filepath):
    """Extract symbols from a single Python file."""
    try:
        with open(filepath, "r") as f:
            source = f.read()
    except (OSError, UnicodeDecodeError):
        return []

    try:
        tree = ast.parse(source, filename=filepath)
    except SyntaxError:
        return []

    symbols = []
    for node in ast.iter_child_nodes(tree):
        sym = node_to_symbol(node, source, is_module_level=True)
        if sym:
            symbols.append(sym)
    return symbols


def node_to_symbol(node, source, is_module_level=False):
    """Convert an AST node to a symbol dict."""
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        decorators = [decorator_name(d) for d in node.decorator_list]
        is_async = isinstance(node, ast.AsyncFunctionDef)
        prefix = "async def" if is_async else "def"
        sig = build_func_signature(node, prefix=prefix)
        return {
            "name": node.name,
            "kind": "function",
            "line": node.lineno,
            "end_line": node.end_lineno or node.lineno,
            "signature": sig,
            "documentation": ast.get_docstring(node) or "",
            "decorators": decorators,
            "is_async": is_async,
            "children": extract_children(node, source),
        }

    elif isinstance(node, ast.ClassDef):
        decorators = [decorator_name(d) for d in node.decorator_list]
        bases = [base_name(b) for b in node.bases]
        is_protocol = any(b in ("Protocol", "typing.Protocol") for b in bases)
        is_abstract = any(b in ("ABC", "abc.ABC", "ABCMeta", "abc.ABCMeta") for b in bases)

        return {
            "name": node.name,
            "kind": "class",
            "line": node.lineno,
            "end_line": node.end_lineno or node.lineno,
            "signature": f"class {node.name}({', '.join(bases)})" if bases else f"class {node.name}",
            "documentation": ast.get_docstring(node) or "",
            "decorators": decorators,
            "bases": bases,
            "is_protocol": is_protocol,
            "is_abstract": is_abstract,
            "children": extract_children(node, source),
        }

    elif isinstance(node, ast.Assign):
        for target in node.targets:
            if isinstance(target, ast.Name):
                # Detect __all__ exports
                if target.id == "__all__" and isinstance(node.value, (ast.List, ast.Tuple)):
                    exports = []
                    for elt in node.value.elts:
                        if isinstance(elt, ast.Constant) and isinstance(elt.value, str):
                            exports.append(elt.value)
                    return {
                        "name": "__all__",
                        "kind": "variable",
                        "line": node.lineno,
                        "end_line": node.end_lineno or node.lineno,
                        "signature": "",
                        "documentation": "",
                        "children": [],
                        "exports": exports,
                    }
                return {
                    "name": target.id,
                    "kind": "variable",
                    "line": node.lineno,
                    "end_line": node.end_lineno or node.lineno,
                    "signature": "",
                    "documentation": "",
                    "children": [],
                }

    elif isinstance(node, ast.AnnAssign):
        # Type-annotated assignment: x: int = 5
        if isinstance(node.target, ast.Name):
            annotation = ast.unparse(node.annotation) if node.annotation else ""
            return {
                "name": node.target.id,
                "kind": "variable",
                "line": node.lineno,
                "end_line": node.end_lineno or node.lineno,
                "signature": f"{node.target.id}: {annotation}" if annotation else "",
                "documentation": "",
                "children": [],
            }

    return None


def extract_children(parent_node, source):
    """Extract child symbols (methods, nested classes, class variables)."""
    children = []
    is_class = isinstance(parent_node, ast.ClassDef)

    for node in ast.iter_child_nodes(parent_node):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            kind = "method" if is_class else "function"
            decorators = [decorator_name(d) for d in node.decorator_list]
            is_async = isinstance(node, ast.AsyncFunctionDef)
            prefix = "async def" if is_async else "def"

            # Detect property, staticmethod, classmethod, abstractmethod
            if "property" in decorators:
                kind = "property"
            elif "staticmethod" in decorators:
                kind = "staticmethod"
            elif "classmethod" in decorators:
                kind = "classmethod"

            is_abstract = "abstractmethod" in decorators or "abc.abstractmethod" in decorators

            children.append({
                "name": node.name,
                "kind": kind,
                "line": node.lineno,
                "end_line": node.end_lineno or node.lineno,
                "signature": build_func_signature(node, prefix=prefix),
                "documentation": ast.get_docstring(node) or "",
                "decorators": decorators,
                "is_abstract": is_abstract,
                "children": [],
            })

        elif isinstance(node, ast.ClassDef):
            children.append({
                "name": node.name,
                "kind": "class",
                "line": node.lineno,
                "end_line": node.end_lineno or node.lineno,
                "signature": f"class {node.name}",
                "documentation": ast.get_docstring(node) or "",
                "decorators": [decorator_name(d) for d in node.decorator_list],
                "children": extract_children(node, source),
            })

        elif is_class and isinstance(node, ast.Assign):
            # Class-level variable: COUNT = 0
            for target in node.targets:
                if isinstance(target, ast.Name):
                    children.append({
                        "name": target.id,
                        "kind": "variable",
                        "line": node.lineno,
                        "end_line": node.end_lineno or node.lineno,
                        "signature": "",
                        "documentation": "",
                        "children": [],
                    })

        elif is_class and isinstance(node, ast.AnnAssign):
            # Class-level annotated variable: name: str = "default"
            if isinstance(node.target, ast.Name):
                annotation = ast.unparse(node.annotation) if node.annotation else ""
                children.append({
                    "name": node.target.id,
                    "kind": "variable",
                    "line": node.lineno,
                    "end_line": node.end_lineno or node.lineno,
                    "signature": f"{node.target.id}: {annotation}" if annotation else "",
                    "documentation": "",
                    "children": [],
                })

    return children


def build_func_signature(node, prefix="def"):
    """Build a function signature string."""
    args = []

    # Handle *args and **kwargs
    for arg in node.args.posonlyargs:
        args.append(_format_arg(arg))

    for arg in node.args.args:
        args.append(_format_arg(arg))

    if node.args.vararg:
        args.append(f"*{_format_arg(node.args.vararg)}")
    elif node.args.kwonlyargs:
        args.append("*")

    for arg in node.args.kwonlyargs:
        args.append(_format_arg(arg))

    if node.args.kwarg:
        args.append(f"**{_format_arg(node.args.kwarg)}")

    sig = f"{prefix} {node.name}({', '.join(args)})"
    if node.returns:
        sig += f" -> {ast.unparse(node.returns)}"
    return sig


def _format_arg(arg):
    """Format a single function argument."""
    name = arg.arg
    if arg.annotation:
        name += f": {ast.unparse(arg.annotation)}"
    return name


def decorator_name(node):
    """Extract the name of a decorator."""
    if isinstance(node, ast.Name):
        return node.id
    elif isinstance(node, ast.Attribute):
        return ast.unparse(node)
    elif isinstance(node, ast.Call):
        return decorator_name(node.func)
    return ast.unparse(node)


def base_name(node):
    """Get the name of a base class node."""
    if isinstance(node, ast.Name):
        return node.id
    elif isinstance(node, ast.Attribute):
        return ast.unparse(node)
    elif isinstance(node, ast.Subscript):
        return ast.unparse(node)
    return "?"


def main():
    if len(sys.argv) < 2:
        print("Usage: python python_symbols.py <file_or_directory>", file=sys.stderr)
        sys.exit(1)

    target = sys.argv[1]
    result = {}

    if os.path.isfile(target):
        result[target] = extract_symbols(target)
    elif os.path.isdir(target):
        for root, dirs, files in os.walk(target):
            dirs[:] = [d for d in dirs if d not in (
                ".venv", "venv", "__pycache__", ".git", "node_modules",
                ".mypy_cache", ".pytest_cache", ".ruff_cache", ".tox",
                "egg-info", ".eggs", "build", "dist",
            ) and not d.endswith(".egg-info")]

            for f in files:
                if f.endswith(".py"):
                    filepath = os.path.join(root, f)
                    rel = os.path.relpath(filepath, target)
                    result[rel] = extract_symbols(filepath)
    else:
        print(f"Not found: {target}", file=sys.stderr)
        sys.exit(1)

    json.dump(result, sys.stdout)


if __name__ == "__main__":
    main()
