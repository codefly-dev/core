#!/usr/bin/env node
import { runNodeJs } from "@bufbuild/protoplugin";
import { protocGenCodeflyFacadeTs } from "./facade.js";

runNodeJs(protocGenCodeflyFacadeTs);
