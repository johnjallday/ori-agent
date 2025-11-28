/**
 * Renderer Primitives
 *
 * Basic drawing primitives and utilities used across all renderer modules.
 * These are low-level canvas operations that don't depend on application state.
 */

export class RendererPrimitives {
  /**
   * @param {CanvasRenderingContext2D} ctx - Canvas 2D context
   */
  constructor(ctx) {
    this.ctx = ctx;
  }

  /**
   * Helper function to draw rounded rectangle
   */
  roundRect(x, y, width, height, radius) {
    this.ctx.beginPath();
    this.ctx.moveTo(x + radius, y);
    this.ctx.lineTo(x + width - radius, y);
    this.ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
    this.ctx.lineTo(x + width, y + height - radius);
    this.ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
    this.ctx.lineTo(x + radius, y + height);
    this.ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
    this.ctx.lineTo(x, y + radius);
    this.ctx.quadraticCurveTo(x, y, x + radius, y);
    this.ctx.closePath();
  }

  /**
   * Helper function to wrap text
   */
  wrapText(text, maxWidth) {
    const words = text.split(' ');
    const lines = [];
    let currentLine = words[0];

    this.ctx.font = '16px system-ui';

    for (let i = 1; i < words.length; i++) {
      const testLine = currentLine + ' ' + words[i];
      const metrics = this.ctx.measureText(testLine);

      if (metrics.width > maxWidth - 40) {
        lines.push(currentLine);
        currentLine = words[i];
      } else {
        currentLine = testLine;
      }
    }
    lines.push(currentLine);
    return lines;
  }

  /**
   * Draw an arrow from (x1, y1) to (x2, y2)
   */
  drawArrow(x1, y1, x2, y2, color, lineWidth = 2, filled = true) {
    const headLength = 20; // Length of arrow head
    const headAngle = Math.PI / 6; // Angle of arrow head (30 degrees)

    // Calculate angle
    const angle = Math.atan2(y2 - y1, x2 - x1);

    // Draw the line (respects current dash pattern)
    this.ctx.beginPath();
    this.ctx.moveTo(x1, y1);
    this.ctx.lineTo(x2, y2);
    this.ctx.strokeStyle = color;
    this.ctx.lineWidth = lineWidth;
    this.ctx.stroke();

    // Save current dash pattern
    const currentDash = this.ctx.getLineDash();

    // Draw the arrow head (always solid for visibility)
    this.ctx.setLineDash([]);

    if (filled) {
      // Filled arrowhead
      this.ctx.beginPath();
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle - headAngle),
        y2 - headLength * Math.sin(angle - headAngle)
      );
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle + headAngle),
        y2 - headLength * Math.sin(angle + headAngle)
      );
      this.ctx.closePath();
      this.ctx.fillStyle = color;
      this.ctx.fill();
    } else {
      // Outlined arrowhead
      this.ctx.beginPath();
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle - headAngle),
        y2 - headLength * Math.sin(angle - headAngle)
      );
      this.ctx.moveTo(x2, y2);
      this.ctx.lineTo(
        x2 - headLength * Math.cos(angle + headAngle),
        y2 - headLength * Math.sin(angle + headAngle)
      );
      this.ctx.strokeStyle = color;
      this.ctx.lineWidth = lineWidth + 1;
      this.ctx.stroke();
    }

    // Restore dash pattern
    this.ctx.setLineDash(currentDash);
  }

  /**
   * Draw a connection port (triangle)
   */
  drawPort(x, y, type, color, orientation = 'auto') {
    this.ctx.save();
    const size = 10;
    const isInput = type === 'input';

    // Determine direction
    let pointUp = true;
    if (orientation === 'down') {
      pointUp = false;
    } else if (orientation === 'up') {
      pointUp = true;
    } else {
      // auto: input points up, output points down
      pointUp = isInput;
    }

    // Triangles: input points upward, output points downward
    const points = pointUp
      ? [
          { x: x, y: y - size },      // top
          { x: x - size, y: y + size },
          { x: x + size, y: y + size },
        ]
      : [
          { x: x, y: y + size },      // bottom
          { x: x - size, y: y - size },
          { x: x + size, y: y - size },
        ];

    // Outer triangle
    this.ctx.beginPath();
    this.ctx.moveTo(points[0].x, points[0].y);
    this.ctx.lineTo(points[1].x, points[1].y);
    this.ctx.lineTo(points[2].x, points[2].y);
    this.ctx.closePath();
    this.ctx.fillStyle = '#ffffff';
    this.ctx.strokeStyle = color;
    this.ctx.lineWidth = 2;
    this.ctx.fill();
    this.ctx.stroke();

    // Inner accent
    const innerShrink = 4;
    const innerPoints = points.map(p => ({
      x: x + (p.x - x) * ((size - innerShrink) / size),
      y: y + (p.y - y) * ((size - innerShrink) / size),
    }));
    this.ctx.beginPath();
    this.ctx.moveTo(innerPoints[0].x, innerPoints[0].y);
    this.ctx.lineTo(innerPoints[1].x, innerPoints[1].y);
    this.ctx.lineTo(innerPoints[2].x, innerPoints[2].y);
    this.ctx.closePath();
    this.ctx.fillStyle = color;
    this.ctx.fill();

    this.ctx.restore();
  }
}
