/*
 * The map-ready character asset contract, expressed as something a machine can
 * check. See docs/CHARACTER_ASSET_PROVENANCE.md ("Map-ready authoring
 * contract") for the prose the rules below enforce.
 *
 * Split out from the test file so the same inspector can be driven by
 * deterministic synthetic fixtures before it is pointed at production art.
 *
 * The two checks here are deliberately *raster* checks: they rasterize the SVG
 * at its native size and look at alpha. That catches anything that paints a
 * full artboard or reaches the edge, whichever primitive drew it, which a
 * source-text check cannot promise. A baked halo that sits wholly inside the
 * safe area is invisible to alpha, so the structural half of the contract lives
 * in internal/web/character_assets_test.go instead. Neither half is sufficient
 * alone.
 */
import sharp from 'sharp';

/*
 * Native render size, transparent margin, and the ceiling on painted area for
 * each variant.
 *
 * `safeInset` is the transparent perimeter every asset must keep clear so ears,
 * props, outlines, and animated extremes cannot clip when the art is composited
 * over an arbitrary surface.
 *
 * `maxPaintedFraction` separates a character from a background. A full card
 * paints 1.00 of the artboard and an inscribed disc paints 0.79; converted
 * character art measures roughly 0.20-0.35. 0.62 sits in the empty middle, so
 * it fails every background idiom without policing how big a character is.
 */
export const ASSET_CONTRACT = {
  portrait: { size: 160, safeInset: 6, maxPaintedFraction: 0.62 },
  sprite: { size: 48, safeInset: 2, maxPaintedFraction: 0.62 },
  static: { size: 48, safeInset: 2, maxPaintedFraction: 0.62 }
};

export const VARIANTS = Object.keys(ASSET_CONTRACT);

/*
 * Rasterize one asset and report every way it breaks the contract.
 *
 * Returns `{ findings, painted }`. `findings` is empty for a compliant asset;
 * each entry carries a `code` so callers can assert on a specific failure mode
 * rather than on message wording.
 *
 * `input` is anything sharp accepts: a path, or a Buffer of SVG source.
 */
export async function inspectAsset(input, variant) {
  const spec = ASSET_CONTRACT[variant];
  if (!spec) {
    throw new Error(
      `unknown variant ${JSON.stringify(variant)}; expected one of ${VARIANTS.join(', ')}`
    );
  }

  let raw;
  try {
    raw = await sharp(input).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
  } catch (err) {
    // A missing file and a malformed SVG both land here, and both mean the same
    // thing to a caller: this asset cannot be shown to a user.
    return {
      findings: [{ code: 'unreadable', detail: `cannot be rasterized: ${err.message}` }],
      painted: null
    };
  }

  const { data, info } = raw;
  const findings = [];

  if (info.width !== spec.size || info.height !== spec.size) {
    findings.push({
      code: 'dimensions',
      detail: `renders at ${info.width}x${info.height}; the contract is ${spec.size}x${spec.size}`
    });
  }

  // Scale the inset with the actual render so a wrong-sized asset still gets a
  // meaningful perimeter report instead of an out-of-bounds read.
  const inset = Math.max(1, Math.round((spec.safeInset * info.width) / spec.size));
  const alphaAt = (x, y) => data[(y * info.width + x) * 4 + 3];

  let intrusions = 0;
  let worst = { alpha: 0, x: 0, y: 0 };
  let painted = 0;

  for (let y = 0; y < info.height; y++) {
    const inTopOrBottomBand = y < inset || y >= info.height - inset;
    for (let x = 0; x < info.width; x++) {
      const alpha = alphaAt(x, y);
      if (alpha > 0) painted++;
      if (alpha === 0) continue;
      const inPerimeter = inTopOrBottomBand || x < inset || x >= info.width - inset;
      if (!inPerimeter) continue;
      intrusions++;
      if (alpha > worst.alpha) worst = { alpha, x, y };
    }
  }

  if (intrusions > 0) {
    findings.push({
      code: 'perimeter',
      detail:
        `${intrusions} painted pixel(s) inside the ${inset}px safe perimeter ` +
        `(worst alpha ${worst.alpha} at ${worst.x},${worst.y}); art must keep the outer ` +
        `${inset}px fully transparent`
    });
  }

  const paintedFraction = painted / (info.width * info.height);
  if (paintedFraction > spec.maxPaintedFraction) {
    findings.push({
      code: 'coverage',
      detail:
        `paints ${(paintedFraction * 100).toFixed(1)}% of the artboard, over the ` +
        `${(spec.maxPaintedFraction * 100).toFixed(0)}% ceiling; this is the signature of a ` +
        `baked card or disc rather than a character`
    });
  }

  return { findings, painted: paintedFraction };
}

/*
 * One-line summary naming the character and variant, so a CI failure says which
 * of 27 files to open rather than only that "an asset" is wrong.
 */
export function formatFindings(id, variant, findings) {
  return findings.map(f => `  ${id}/${variant}.svg [${f.code}] ${f.detail}`).join('\n');
}
