import { Card } from '@roedu/ui';
import { ActivityCard } from '../components/ActivityCard';
import { SmartLink } from '../components/SmartLink';
import type { ScreenProps } from './registry';
import type { CommunityDetailData } from './types3';

export function CommunityDetailScreen({ payload }: ScreenProps) {
  const data = payload.data as CommunityDetailData;
  const { ui } = data;

  return (
    <div className="sa-screen">
      <p className="muted">
        <SmartLink href={data.urls.communities}>← {ui.back}</SmartLink>
      </p>
      <h1>{data.name}</h1>
      <p className="muted">{data.lead}</p>
      {data.linkedGroup && (
        <Card>
          <SmartLink href={data.linkedGroup.url}>{data.linkedGroup.label}</SmartLink>
        </Card>
      )}

      {data.cards.length > 0 ? (
        <div className="sa-card-grid">
          {data.cards.map((card) => (
            <ActivityCard key={card.pk} a={card} />
          ))}
        </div>
      ) : (
        <p className="sa-empty">
          {ui.empty} <SmartLink href={data.urls.organizeNew}>{ui.organise}</SmartLink>
        </p>
      )}
    </div>
  );
}
