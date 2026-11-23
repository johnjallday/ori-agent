// A real one-page PDF with an image XObject (one gray pixel) and no text
// layer. Offsets are measured in bytes rather than approximated fixture data.
export function imageOnlyDocument() {
  const image = 'q 12 0 0 12 0 0 cm /Img Do Q';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Count 1 /Kids [3 0 R] >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] /Resources << /XObject << /Img 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceGray /Length 1 >>\nstream\nX\nendstream',
    `<< /Length ${image.length} >>\nstream\n${image}\nendstream`
  ];
  let contents = '%PDF-1.4\n';
  const offsets = ['0000000000 65535 f '];
  for (const [i, object] of objects.entries()) {
    offsets.push(`${String(contents.length).padStart(10, '0')} 00000 n `);
    contents += `${i + 1} 0 obj\n${object}\nendobj\n`;
  }
  return Buffer.from(
    `${contents}xref\n0 6\n${offsets.join('\n')}\ntrailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${contents.length}\n%%EOF\n`,
    'ascii'
  );
}
