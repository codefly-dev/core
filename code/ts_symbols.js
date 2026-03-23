/**
 * Extract symbols from TypeScript/JavaScript source files using the TypeScript compiler API.
 *
 * Called by TSASTSymbolProvider via subprocess. Outputs JSON to stdout.
 *
 * Usage:
 *   node ts_symbols.js <file_or_directory>
 *
 * If given a file, outputs symbols for that file.
 * If given a directory, outputs symbols for all .ts/.tsx/.js/.jsx files in it.
 *
 * Extracts: functions, classes, interfaces, type aliases, exported variables,
 * methods, properties, arrow functions assigned to const, React components.
 */

const fs = require("fs");
const path = require("path");

// Try to find typescript - check target dir, NODE_PATH, global
let ts;
const target = process.argv[2] || ".";
const targetDir = fs.statSync(target).isDirectory() ? target : path.dirname(target);
const tryPaths = [
  path.join(targetDir, "node_modules", "typescript"),
  "typescript",
];
for (const p of tryPaths) {
  try { ts = require(p); break; } catch {}
}
if (!ts) {
  console.error("Cannot find 'typescript' module. Install it: npm install -g typescript");
  process.exit(1);
}

const SKIP_DIRS = new Set([
  "node_modules", ".git", "dist", "build", ".next", ".nuxt",
  "coverage", ".cache", "__tests__", "__mocks__", ".turbo",
  ".svelte-kit", ".output",
]);

const TS_EXTENSIONS = new Set([".ts", ".tsx", ".js", ".jsx"]);

function extractSymbols(filepath) {
  let source;
  try {
    source = fs.readFileSync(filepath, "utf-8");
  } catch (e) {
    return [];
  }

  const ext = path.extname(filepath).toLowerCase();
  let scriptKind;
  switch (ext) {
    case ".tsx": scriptKind = ts.ScriptKind.TSX; break;
    case ".jsx": scriptKind = ts.ScriptKind.JSX; break;
    case ".js":  scriptKind = ts.ScriptKind.JS; break;
    default:     scriptKind = ts.ScriptKind.TS; break;
  }

  let sourceFile;
  try {
    sourceFile = ts.createSourceFile(
      filepath,
      source,
      ts.ScriptTarget.Latest,
      /* setParentNodes */ true,
      scriptKind
    );
  } catch (e) {
    return [];
  }

  const symbols = [];
  ts.forEachChild(sourceFile, function visit(node) {
    const sym = nodeToSymbol(node, source, sourceFile);
    if (sym) {
      symbols.push(sym);
    }
  });
  return symbols;
}

function nodeToSymbol(node, source, sourceFile) {
  const isExported = hasExportModifier(node);

  // Function declaration: function foo() {} or export function foo() {}
  if (ts.isFunctionDeclaration(node) && node.name) {
    return {
      name: node.name.text,
      kind: "function",
      line: getLine(node, sourceFile),
      end_line: getEndLine(node, sourceFile),
      signature: buildFunctionSignature(node, source, sourceFile),
      documentation: getJSDoc(node, source, sourceFile),
      is_exported: isExported,
      is_async: hasAsyncModifier(node),
      is_default_export: hasDefaultModifier(node),
      children: extractChildren(node, source, sourceFile),
    };
  }

  // Class declaration
  if (ts.isClassDeclaration(node)) {
    const name = node.name ? node.name.text : "(anonymous)";
    const heritage = getHeritage(node);
    return {
      name: name,
      kind: "class",
      line: getLine(node, sourceFile),
      end_line: getEndLine(node, sourceFile),
      signature: buildClassSignature(node),
      documentation: getJSDoc(node, source, sourceFile),
      is_exported: isExported,
      is_default_export: hasDefaultModifier(node),
      bases: heritage.extends,
      implements: heritage.implements,
      children: extractChildren(node, source, sourceFile),
    };
  }

  // Interface declaration
  if (ts.isInterfaceDeclaration(node)) {
    const heritage = getInterfaceHeritage(node);
    return {
      name: node.name.text,
      kind: "interface",
      line: getLine(node, sourceFile),
      end_line: getEndLine(node, sourceFile),
      signature: `interface ${node.name.text}`,
      documentation: getJSDoc(node, source, sourceFile),
      is_exported: isExported,
      bases: heritage,
      children: extractChildren(node, source, sourceFile),
    };
  }

  // Type alias: type Foo = ...
  if (ts.isTypeAliasDeclaration(node)) {
    return {
      name: node.name.text,
      kind: "type",
      line: getLine(node, sourceFile),
      end_line: getEndLine(node, sourceFile),
      signature: `type ${node.name.text}`,
      documentation: getJSDoc(node, source, sourceFile),
      is_exported: isExported,
      children: [],
    };
  }

  // Enum declaration
  if (ts.isEnumDeclaration(node)) {
    return {
      name: node.name.text,
      kind: "type",
      line: getLine(node, sourceFile),
      end_line: getEndLine(node, sourceFile),
      signature: `enum ${node.name.text}`,
      documentation: getJSDoc(node, source, sourceFile),
      is_exported: isExported,
      children: extractChildren(node, source, sourceFile),
    };
  }

  // Variable statement: const/let/var
  // Handles: export const Foo = () => {}, export const Bar = 42, etc.
  if (ts.isVariableStatement(node)) {
    const stmtExported = hasExportModifier(node);
    const results = [];
    for (const decl of node.declarationList.declarations) {
      if (!ts.isIdentifier(decl.name)) continue;
      const name = decl.name.text;
      const init = decl.initializer;

      // Arrow function or function expression assigned to const
      if (init && (ts.isArrowFunction(init) || ts.isFunctionExpression(init))) {
        const isComponent = isReactComponent(name, init, source, sourceFile);
        results.push({
          name: name,
          kind: isComponent ? "function" : "function",
          line: getLine(node, sourceFile),
          end_line: getEndLine(node, sourceFile),
          signature: buildArrowSignature(name, init, source, sourceFile),
          documentation: getJSDoc(node, source, sourceFile),
          is_exported: stmtExported,
          is_async: hasAsyncModifier(init),
          is_react_component: isComponent,
          children: [],
        });
      } else {
        // Regular variable/const
        results.push({
          name: name,
          kind: "variable",
          line: getLine(node, sourceFile),
          end_line: getEndLine(node, sourceFile),
          signature: "",
          documentation: getJSDoc(node, source, sourceFile),
          is_exported: stmtExported,
          children: [],
        });
      }
    }
    // Return first symbol (common case), or null
    return results.length === 1 ? results[0] : results.length > 0 ? results[0] : null;
  }

  // Export default function() {} (unnamed)
  if (ts.isExportAssignment(node) && !node.isExportEquals) {
    const expr = node.expression;
    if (ts.isArrowFunction(expr) || ts.isFunctionExpression(expr)) {
      return {
        name: "default",
        kind: "function",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: "export default function",
        documentation: getJSDoc(node, source, sourceFile),
        is_exported: true,
        is_default_export: true,
        children: [],
      };
    }
  }

  return null;
}

function extractChildren(parentNode, source, sourceFile) {
  const children = [];
  const isClass = ts.isClassDeclaration(parentNode);

  ts.forEachChild(parentNode, function visitChild(node) {
    // Method declaration (class)
    if (isClass && ts.isMethodDeclaration(node) && node.name) {
      const name = ts.isIdentifier(node.name) ? node.name.text :
                   ts.isStringLiteral(node.name) ? node.name.text : node.name.getText(sourceFile);
      children.push({
        name: name,
        kind: "method",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: buildMethodSignature(node, source, sourceFile),
        documentation: getJSDoc(node, source, sourceFile),
        is_static: hasStaticModifier(node),
        is_async: hasAsyncModifier(node),
        visibility: getVisibility(node),
        children: [],
      });
    }

    // Constructor
    if (isClass && ts.isConstructorDeclaration(node)) {
      children.push({
        name: "constructor",
        kind: "method",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: "constructor" + getParameterList(node, source, sourceFile),
        documentation: getJSDoc(node, source, sourceFile),
        children: [],
      });
    }

    // Property declaration (class)
    if (isClass && ts.isPropertyDeclaration(node) && node.name) {
      const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(sourceFile);
      children.push({
        name: name,
        kind: "property",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: "",
        documentation: getJSDoc(node, source, sourceFile),
        is_static: hasStaticModifier(node),
        visibility: getVisibility(node),
        children: [],
      });
    }

    // Getter/Setter
    if (isClass && ts.isGetAccessorDeclaration(node) && node.name) {
      const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(sourceFile);
      children.push({
        name: name,
        kind: "property",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: `get ${name}`,
        documentation: getJSDoc(node, source, sourceFile),
        children: [],
      });
    }

    // Interface members: property signatures, method signatures
    if (ts.isInterfaceDeclaration(parentNode)) {
      if (ts.isPropertySignature(node) && node.name) {
        const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(sourceFile);
        children.push({
          name: name,
          kind: "property",
          line: getLine(node, sourceFile),
          end_line: getEndLine(node, sourceFile),
          signature: "",
          documentation: getJSDoc(node, source, sourceFile),
          children: [],
        });
      }
      if (ts.isMethodSignature(node) && node.name) {
        const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(sourceFile);
        children.push({
          name: name,
          kind: "method",
          line: getLine(node, sourceFile),
          end_line: getEndLine(node, sourceFile),
          signature: buildMethodSignature(node, source, sourceFile),
          documentation: getJSDoc(node, source, sourceFile),
          children: [],
        });
      }
    }

    // Enum members
    if (ts.isEnumDeclaration(parentNode) && ts.isEnumMember(node)) {
      const name = ts.isIdentifier(node.name) ? node.name.text : node.name.getText(sourceFile);
      children.push({
        name: name,
        kind: "variable",
        line: getLine(node, sourceFile),
        end_line: getEndLine(node, sourceFile),
        signature: "",
        documentation: "",
        children: [],
      });
    }
  });

  return children;
}

// ── Helpers ──────────────────────────────────────────────

function getLine(node, sourceFile) {
  return sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
}

function getEndLine(node, sourceFile) {
  return sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1;
}

function getJSDoc(node, source, sourceFile) {
  // Check for JSDoc-style comments
  const ranges = ts.getLeadingCommentRanges(source, node.getFullStart());
  if (!ranges) return "";
  for (const range of ranges) {
    const text = source.slice(range.pos, range.end);
    if (text.startsWith("/**")) {
      // Strip /** ... */ and leading * from each line
      return text
        .replace(/^\/\*\*\s*/, "")
        .replace(/\s*\*\/$/, "")
        .split("\n")
        .map(line => line.replace(/^\s*\*\s?/, ""))
        .join("\n")
        .trim();
    }
  }
  return "";
}

function hasExportModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.ExportKeyword);
}

function hasDefaultModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.DefaultKeyword);
}

function hasAsyncModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.AsyncKeyword);
}

function hasStaticModifier(node) {
  if (!node.modifiers) return false;
  return node.modifiers.some(m => m.kind === ts.SyntaxKind.StaticKeyword);
}

function getVisibility(node) {
  if (!node.modifiers) return "public";
  for (const m of node.modifiers) {
    if (m.kind === ts.SyntaxKind.PrivateKeyword) return "private";
    if (m.kind === ts.SyntaxKind.ProtectedKeyword) return "protected";
  }
  return "public";
}

function getParameterList(node, source, sourceFile) {
  if (!node.parameters || node.parameters.length === 0) return "()";
  const params = node.parameters.map(p => p.getText(sourceFile));
  return `(${params.join(", ")})`;
}

function buildFunctionSignature(node, source, sourceFile) {
  const async = hasAsyncModifier(node) ? "async " : "";
  const name = node.name ? node.name.text : "anonymous";
  const params = getParameterList(node, source, sourceFile);
  let sig = `${async}function ${name}${params}`;
  if (node.type) {
    sig += `: ${node.type.getText(sourceFile)}`;
  }
  return sig;
}

function buildArrowSignature(name, node, source, sourceFile) {
  const async = hasAsyncModifier(node) ? "async " : "";
  const params = getParameterList(node, source, sourceFile);
  let sig = `${async}const ${name} = ${params}`;
  if (node.type) {
    sig += `: ${node.type.getText(sourceFile)}`;
  }
  return sig;
}

function buildMethodSignature(node, source, sourceFile) {
  const async = hasAsyncModifier(node) ? "async " : "";
  const stat = hasStaticModifier(node) ? "static " : "";
  const name = ts.isIdentifier(node.name) ? node.name.text :
               ts.isStringLiteral(node.name) ? node.name.text : node.name.getText(sourceFile);
  const params = getParameterList(node, source, sourceFile);
  let sig = `${stat}${async}${name}${params}`;
  if (node.type) {
    sig += `: ${node.type.getText(sourceFile)}`;
  }
  return sig;
}

function buildClassSignature(node) {
  const name = node.name ? node.name.text : "(anonymous)";
  const heritage = getHeritage(node);
  let sig = `class ${name}`;
  if (heritage.extends.length > 0) {
    sig += ` extends ${heritage.extends.join(", ")}`;
  }
  if (heritage.implements.length > 0) {
    sig += ` implements ${heritage.implements.join(", ")}`;
  }
  return sig;
}

function getHeritage(node) {
  const result = { extends: [], implements: [] };
  if (!node.heritageClauses) return result;
  for (const clause of node.heritageClauses) {
    const names = clause.types.map(t => t.expression.getText());
    if (clause.token === ts.SyntaxKind.ExtendsKeyword) {
      result.extends.push(...names);
    } else if (clause.token === ts.SyntaxKind.ImplementsKeyword) {
      result.implements.push(...names);
    }
  }
  return result;
}

function getInterfaceHeritage(node) {
  const bases = [];
  if (!node.heritageClauses) return bases;
  for (const clause of node.heritageClauses) {
    for (const type of clause.types) {
      bases.push(type.expression.getText());
    }
  }
  return bases;
}

function isReactComponent(name, init, source, sourceFile) {
  // Heuristic: PascalCase name + returns JSX
  if (!/^[A-Z]/.test(name)) return false;
  const text = init.getText(sourceFile);
  return text.includes("<") && (text.includes("/>") || text.includes("</"));
}

// ── Main ──────────────────────────────────────────────────

function walkDirectory(dir, baseDir) {
  const result = {};
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      Object.assign(result, walkDirectory(fullPath, baseDir));
    } else if (entry.isFile() && TS_EXTENSIONS.has(path.extname(entry.name).toLowerCase())) {
      // Skip .d.ts declaration files
      if (entry.name.endsWith(".d.ts")) continue;
      const rel = path.relative(baseDir, fullPath);
      result[rel] = extractSymbols(fullPath);
    }
  }
  return result;
}

function main() {
  if (process.argv.length < 3) {
    process.stderr.write("Usage: node ts_symbols.js <file_or_directory>\n");
    process.exit(1);
  }

  const target = process.argv[2];
  let result = {};

  try {
    const stat = fs.statSync(target);
    if (stat.isFile()) {
      result[target] = extractSymbols(target);
    } else if (stat.isDirectory()) {
      result = walkDirectory(target, target);
    }
  } catch (e) {
    process.stderr.write(`Not found: ${target}\n`);
    process.exit(1);
  }

  process.stdout.write(JSON.stringify(result));
}

main();
