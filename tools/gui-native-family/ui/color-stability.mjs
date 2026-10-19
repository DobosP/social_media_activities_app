// Two observed samples, not an all-frame accessibility certification.
export function initialColorsEqualSettled(before, after) {
  return Array.isArray(before) && Array.isArray(after) && before.length === after.length && before.every((initial, index) => {
    const settled = after[index];
    return initial && settled && initial.action === settled.action && initial.label === settled.label &&
      typeof initial.color === 'string' && initial.color.length > 0 && typeof initial.backgroundColor === 'string' && initial.backgroundColor.length > 0 &&
      initial.color === settled.color && initial.backgroundColor === settled.backgroundColor;
  });
}
