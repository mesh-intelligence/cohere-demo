// The turn context the panel supplies to the form contract (srd016 R3.1).
//
// The contract fills only from the response and from context the caller
// states explicitly, so what the panel may state is exactly what it knows
// about the turn it just ran: the tasking is the message the analyst sent,
// and the date-time group is when the answer arrived. The originator is
// deliberately absent -- the panel holds no analyst identity, and R3.2 bars
// filling the field with a plausible one; it renders empty until a deployment
// with real authentication has an identity to state.

import type { TurnContext } from "./intsumForm";

const DTG_MONTHS = [
  "JAN", "FEB", "MAR", "APR", "MAY", "JUN",
  "JUL", "AUG", "SEP", "OCT", "NOV", "DEC",
];

// formatDateTimeGroup renders a moment as a military date-time group,
// DDHHMMZ MON YY, always in Zulu time so two analysts reading the same brief
// read the same moment.
export function formatDateTimeGroup(at: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  const day = pad(at.getUTCDate());
  const time = pad(at.getUTCHours()) + pad(at.getUTCMinutes());
  const month = DTG_MONTHS[at.getUTCMonth()];
  const year = pad(at.getUTCFullYear() % 100);
  return `${day}${time}Z ${month} ${year}`;
}

// turnContextFor states what the panel knows about one completed turn.
export function turnContextFor(tasking: string, completedAt: Date): TurnContext {
  return {
    tasking,
    dateTimeGroup: formatDateTimeGroup(completedAt),
  };
}
