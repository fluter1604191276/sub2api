import assert from "node:assert/strict";
import {
  classifyComplianceRequest,
  hasRegionAck,
  isMachinePath,
} from "../src/index.js";

function classify(overrides) {
  return classifyComplianceRequest({
    hostname: "fluterapi.top",
    pathname: "/",
    country: "CN",
    cookie: "",
    ...overrides,
  });
}

assert.equal(hasRegionAck("a=1; fluter_region_ack=1; theme=dark"), true);
assert.equal(hasRegionAck("a=1; fluter_region_ack=0"), false);

assert.equal(classify({ country: "US" }), "pass");
assert.equal(classify({ hostname: "fluterapi.top", pathname: "/" }), "main-blocked");
assert.equal(classify({ hostname: "fluterapi.top", pathname: "/docs/" }), "main-blocked");
assert.equal(classify({ hostname: "fluterapi.top", pathname: "/admin/upstream-rates/" }), "pass");
assert.equal(classify({ hostname: "fluterapi.top", pathname: "/admin/upstream-rates/index.html" }), "pass");

assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/" }), "risk-notice");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/console" }), "risk-notice");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/health" }), "pass");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/healthz" }), "risk-notice");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/v1" }), "pass");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/v1/models" }), "pass");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/v1/chat/completions" }), "pass");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/responses" }), "pass");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/responses-anything" }), "risk-notice");
assert.equal(classify({ hostname: "api.fluterapi.top", pathname: "/docs", cookie: "fluter_region_ack=1" }), "pass");

assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/" }), "risk-notice");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/health" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/responses" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/responses" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/images/generations" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/images/generations/stream" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/images/generations-preview" }), "risk-notice");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/images/edits" }), "pass");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/v1/models" }), "risk-notice");
assert.equal(classify({ hostname: "img-api.fluterapi.top", pathname: "/guide", cookie: "x=1; fluter_region_ack=1" }), "pass");

assert.equal(isMachinePath("api.fluterapi.top", "/v1/models"), true);
assert.equal(isMachinePath("img-api.fluterapi.top", "/v1/models"), false);

console.log("compliance Worker route tests passed");
