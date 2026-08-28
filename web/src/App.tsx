import { Navigate, Route, Routes } from "react-router-dom";

import { NotFoundPage } from "./pages/NotFoundPage";
import { QueueBoardPage } from "./pages/QueueBoardPage";

// The URL space mirrors the server-rendered pages it sits beside, so a
// customer holding either kind of link recognises the other:
//
//   /:org                       what this business offers
//   /:org/book/:service         pick a time
//   /:org/appointments/:id      a booking's own page
//   /:org/q/:queue              a walk-in line's board, and the join form
//   /:org/q/:queue/t/:ticket    a customer's own number
//   /:org/q/:queue/display      the waiting-room screen
//
// There is deliberately no route at "/". This front end is a customer's
// view of one business, reached from a QR code or a link that names it;
// a root page would be a directory of who uses the product, which is the
// same thing the API's 404s are careful not to become.
export function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/not-found" replace />} />
      <Route path="/:orgSlug/q/:queueSlug" element={<QueueBoardPage />} />
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  );
}
