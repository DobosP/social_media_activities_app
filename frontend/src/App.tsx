import { Badge, Button, Card, Container, Stack } from '@roedu/ui';
import { brandGradient } from './theme';
import { useCspSafeStyle } from '@roedu/ui';

/**
 * Phase 1: a design-preview screen (DEBUG-only route /app/preview/) proving the
 * Vite↔Django pipeline, the Aurora theme, and CSP-safe rendering end to end.
 * Real screens replace this in Phase 2.
 */
export function App() {
  const heroRef = useCspSafeStyle<HTMLDivElement>({
    background: brandGradient,
    borderRadius: 'var(--roedu-radius-lg)',
    padding: 'var(--roedu-space-xl)',
    color: '#101223',
  });
  return (
    <Container>
      <Stack gap="lg">
        <div ref={heroRef}>
          <h1>Aurora Social</h1>
          <p>Previzualizare a identității vizuale — React + @roedu/ui, servit de Django.</p>
        </div>
        <Card>
          <Stack gap="md">
            <h2>Butoane</h2>
            <Stack direction="row" gap="sm" wrap>
              <Button>Participă</Button>
              <Button variant="secondary">Detalii</Button>
              <Button variant="ghost">Mai târziu</Button>
              <Button variant="danger">Raportează</Button>
            </Stack>
            <h2>Insigne</h2>
            <Stack direction="row" gap="sm" wrap>
              <Badge tone="primary">Sport</Badge>
              <Badge tone="accent">În aer liber</Badge>
              <Badge tone="success">Locuri libere</Badge>
              <Badge tone="neutral">Cluj-Napoca</Badge>
            </Stack>
          </Stack>
        </Card>
      </Stack>
    </Container>
  );
}
