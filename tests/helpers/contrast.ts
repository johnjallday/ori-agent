// contrastOfText is the WCAG contrast ratio of an element's text against its
// own background colour. Run it in the page (locator.evaluate). It ignores
// alpha, so use it only where the element paints an opaque background.
export function contrastOfText(node: Element) {
  const rgb = (value: string) => (value.match(/\d+(\.\d+)?/g) || []).slice(0, 3).map(Number);
  const luminance = ([r, g, b]: number[]) => {
    const [R, G, B] = [r, g, b].map(channel => {
      const c = channel / 255;
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * R + 0.7152 * G + 0.0722 * B;
  };
  const style = getComputedStyle(node);
  const [light, dark] = [luminance(rgb(style.color)), luminance(rgb(style.backgroundColor))].sort(
    (a, b) => b - a
  );
  return (light + 0.05) / (dark + 0.05);
}
