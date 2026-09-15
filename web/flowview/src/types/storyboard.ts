export interface StoryFrame {
  frameOrdinal: string;
  gatewayRole: string;
  businessTitle: string;
  businessNarrative: string;
  technicalAnchor: string;
  stepId: string;
  surgeryBadge?: 'ACTIVE SURGERY';
  deltaTag?: '+ NEW SURGERY' | '~ RULE CHG' | '- REMOVED';
  isHighlighted?: boolean;
}
