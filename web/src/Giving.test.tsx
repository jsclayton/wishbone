import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Gift, Occasion } from './api'
import { aGift, ada, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const GIVING = 'GET /api/v2/gifts?giver_id=me&state=open'
const mom = { id: '9', name: 'Mom', avatar: null }
const asAda = { [ME]: { status: 200, body: ada } }
const giving = (...gifts: Gift[]) => ({ ...asAda, [GIVING]: page(gifts) })
const mug: Gift = { ...aGift, id: '61', title: 'A mug', item: { id: '22', title: 'A mug', primary_image: null }, quantity: 2 }
const occasion = (id: string, name: string, kind: Occasion['kind'], next: string): Occasion => ({
  id,
  kind,
  name,
  rule: { kind: 'annual', month: 12, day: 6, year: null, weekday: null, n: null },
  user: mom,
  is_everyone: null,
  next_date: next,
  can_edit: false,
  can_delete: false,
  can_attach_group: false,
})

describe('Giving', () => {
  test('groups the open gifts by recipient and occasion, and a hidden occasion by its reveal date', async () => {
    const hidden: Gift = { ...aGift, id: '62', title: 'A rug', occasion: null, occasion_name: null, occasion_date: '2027-03-03' }
    serve(giving(aGift, mug, hidden))
    open('/giving')

    expect(await heading('Giving')).toBeInTheDocument()
    const groups = await screen.findAllByRole('region')
    expect(groups.map((g) => g.getAttribute('aria-label'))).toEqual(['Mom, First Advent', 'Mom, hidden until Mar 4'])
    expect(within(groups[0]!).getAllByRole('heading', { level: 3 })).toHaveLength(2)
  })

  test('names a birthday by its user when the list has several recipients', async () => {
    const dad = { id: '10', name: 'Dad', avatar: null }
    const birthday: Gift = { ...aGift, occasion: { id: '33', kind: 'birthday', name: 'Birthday' }, occasion_name: 'Birthday', recipients: [mom, dad] }
    serve({
      ...giving(birthday),
      'GET /api/v2/occasions?list_id=12': page([{ ...occasion('33', 'Birthday', 'birthday', '2027-01-01'), user: dad }]),
    })
    open('/giving')

    expect(await screen.findByRole('region', { name: 'Dad, birthday' })).toBeInTheDocument()
  })

  test('shows each gift with its title, its quantity, and when it is hidden until', async () => {
    serve(giving(aGift, mug))
    open('/giving')

    const group = await screen.findByRole('region', { name: 'Mom, First Advent' })
    expect(within(group).getByRole('heading', { level: 3, name: 'A kettle' })).toBeInTheDocument()
    expect(within(group).getByRole('heading', { level: 3, name: 'A mug (2)' })).toBeInTheDocument()
    expect(within(group).getAllByText((_, el) => el?.tagName === 'P' && el.textContent === 'Hidden until Dec 7')).toHaveLength(2)
  })

  test('says Nothing yet, with a way to find a list, when there are no gifts', async () => {
    serve(giving())
    open('/giving')

    expect(await screen.findByRole('heading', { level: 2, name: 'Nothing yet' })).toBeInTheDocument()
    expect(screen.getByText("Tap I'll get this on an item and it shows up here. Only you can see it.")).toBeInTheDocument()
  })

  test('puts the gifts that need attention first', async () => {
    const later: Gift = {
      ...aGift,
      id: '63',
      title: 'Socks',
      occasion: { id: '40', kind: 'custom', name: 'Later' },
      occasion_name: 'Later',
      occasion_date: '2027-01-01',
      needs_attention: true,
    }
    serve(giving(aGift, later))
    open('/giving')

    const groups = await screen.findAllByRole('region')
    expect(groups.map((g) => g.getAttribute('aria-label'))).toEqual(['Mom, Later', 'Mom, First Advent'])
  })

  test('shows the server\'s words when the gifts cannot load', async () => {
    serve({ ...asAda, [GIVING]: problem(500, 'internal_error') })
    open('/giving')

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load your gifts. Reload and try again.')
  })

  test('Bought and Not bought mark the gift, and the screen shows what the server kept', async () => {
    const user = userEvent.setup()
    let bought = false
    const fetch = serve({
      ...asAda,
      [GIVING]: () => page([{ ...aGift, is_bought: bought }]),
      'PATCH /api/v2/gifts/60': () => {
        bought = true
        return { status: 200, body: { ...aGift, is_bought: true } }
      },
    })
    open('/giving')

    const notBought = await screen.findByRole('button', { name: 'Not bought: A kettle' })
    expect(notBought).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: 'Bought: A kettle' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/gifts/60')).toEqual({ is_bought: true }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Bought: A kettle' })).toHaveAttribute('aria-pressed', 'true'))
  })

  test('How many raises and lowers the quantity, and cannot raise when the server says it cannot', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...giving(mug, { ...aGift, id: '64', title: 'Socks', can_raise_quantity: false, quantity: 2 }), 'PATCH /api/v2/gifts/61': { status: 200, body: mug } })
    open('/giving')

    await user.click(await screen.findByRole('button', { name: 'One more: A mug' }))
    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/gifts/61')).toEqual({ quantity: 3 }))
    expect(screen.getByRole('button', { name: 'One more: Socks' })).toHaveAttribute('aria-disabled', 'true')
    await user.click(screen.getByRole('button', { name: 'One more: Socks' }))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/gifts/64', expect.anything())
  })

  test('says why a change was refused, and keeps the gift as it was', async () => {
    const user = userEvent.setup()
    serve({ ...giving(mug), 'PATCH /api/v2/gifts/61': problem(409, 'item_fully_claimed', 'Someone else just claimed some of this, so not that many are still wanted.') })
    open('/giving')

    await user.click(await screen.findByRole('button', { name: 'One more: A mug' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Someone else just claimed some of this, so not that many are still wanted.')
    expect(screen.getByRole('heading', { level: 3, name: 'A mug (2)' })).toBeInTheDocument()
  })

  test('saves a private note, and who the gift is from', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...giving(aGift), 'PATCH /api/v2/gifts/60': { status: 200, body: aGift } })
    open('/giving')

    await user.click(await screen.findByRole('button', { name: 'Add a note: A kettle' }))
    await user.type(screen.getByRole('textbox', { name: 'Private note: A kettle' }), 'The blue one')
    await user.click(screen.getByRole('button', { name: 'Save note: A kettle' }))
    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/gifts/60')).toEqual({ note: 'The blue one' }))

    await user.click(await screen.findByRole('button', { name: 'Add who it is from: A kettle' }))
    await user.type(screen.getByRole('textbox', { name: 'From: A kettle' }), 'Emma')
    await user.click(screen.getByRole('button', { name: 'Save from: A kettle' }))
    const patches = () => fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH').map(([, init]) => JSON.parse(String(init?.body)))
    await waitFor(() => expect(patches()).toEqual([{ note: 'The blue one' }, { from_label: 'Emma' }]))
  })

  test('shows a note and a from label, and clears one with an empty field', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...giving({ ...aGift, note: 'The blue one', from_label: 'Emma' }), 'PATCH /api/v2/gifts/60': { status: 200, body: aGift } })
    open('/giving')

    expect(await screen.findByText('The blue one')).toBeInTheDocument()
    expect(screen.getByText('From Emma')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Edit who it is from: A kettle' }))
    await user.clear(screen.getByRole('textbox', { name: 'From: A kettle' }))
    await user.click(screen.getByRole('button', { name: 'Save from: A kettle' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/gifts/60')).toEqual({ from_label: null }))
  })

  test('Change occasion offers the occasions the giver can pick, and saves the one chosen', async () => {
    const user = userEvent.setup()
    const advent = occasion('31', 'First Advent', 'custom', '2026-12-06')
    const birthday = occasion('32', 'Birthday', 'birthday', '2027-01-01')
    const fetch = serve({
      ...giving(aGift),
      'GET /api/v2/occasions?list_id=12': page([advent, birthday]),
      'PATCH /api/v2/gifts/60': { status: 200, body: { ...aGift, occasion: { id: '32', kind: 'birthday', name: 'Birthday' } } },
    })
    open('/giving')

    await user.click(await screen.findByRole('button', { name: 'Change occasion: A kettle' }))
    await user.selectOptions(await screen.findByRole('combobox', { name: 'Occasion' }), '32')
    await user.click(screen.getByRole('button', { name: 'Save occasion: A kettle' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/gifts/60')).toEqual({ occasion_id: '32' }))
  })

  test('Change occasion says the occasion is picked for a giver who can see none of the other recipients', async () => {
    const user = userEvent.setup()
    serve({ ...giving(aGift), 'GET /api/v2/occasions?list_id=12': page([]) })
    open('/giving')

    await user.click(await screen.findByRole('button', { name: 'Change occasion: A kettle' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('You can see none of the other recipients, so the occasion is picked for you.')
    expect(screen.queryByRole('button', { name: 'Save occasion: A kettle' })).not.toBeInTheDocument()
  })

  describe('flags', () => {
    const changed: Gift = {
      ...aGift,
      needs_attention: true,
      can_acknowledge: true,
      flags: [{ code: 'changed', fields: [{ field: 'title', old: 'A kettle', new: 'A teapot' }] }],
    }
    const removed: Gift = { ...aGift, needs_attention: true, can_acknowledge: true, flags: [{ code: 'removed' }] }

    test('a gift with no flags shows no flag buttons', async () => {
      serve(giving(aGift))
      open('/giving')

      await screen.findByRole('heading', { level: 3, name: 'A kettle' })
      expect(screen.queryByRole('button', { name: /Got it|Keep it/ })).not.toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /Drop it/ })).not.toBeInTheDocument()
    })

    test('a changed gift says what changed, and Got it clears the flag', async () => {
      const user = userEvent.setup()
      let flagged = true
      const fetch = serve({
        ...asAda,
        [GIVING]: () => page([flagged ? changed : aGift]),
        'POST /api/v2/gifts/60/acknowledge': () => {
          flagged = false
          return { status: 200, body: aGift }
        },
      })
      open('/giving')

      const strip = (await screen.findByText('Changed since you picked it.')).parentElement!
      expect(strip).toHaveTextContent('Changed since you picked it. Title went from “A kettle” to “A teapot”.')
      await user.click(screen.getByRole('button', { name: 'Got it, changes: A kettle' }))

      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['changed'] }))
      await waitFor(() => expect(screen.queryByText('Changed since you picked it.')).not.toBeInTheDocument())
    })

    test('a removed gift can be kept or dropped', async () => {
      const user = userEvent.setup()
      let flagged = true
      const fetch = serve({
        ...asAda,
        [GIVING]: () => page([flagged ? removed : aGift]),
        'POST /api/v2/gifts/60/acknowledge': () => {
          flagged = false
          return { status: 200, body: aGift }
        },
      })
      open('/giving')

      expect(await screen.findByText('Removed from the list.')).toBeInTheDocument()
      expect(screen.getByText(/You can still give it, or drop it\./)).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Drop it: A kettle' })).toHaveAttribute('href', '/gifts/60/unclaim')
      await user.click(screen.getByRole('button', { name: 'Keep it: A kettle' }))

      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['removed'] }))
      await waitFor(() => expect(screen.queryByText('Removed from the list.')).not.toBeInTheDocument())
    })

    test('a moved gift names the new list, or says the list is out of reach', async () => {
      const user = userEvent.setup()
      const visible: Gift = { ...aGift, can_acknowledge: true, flags: [{ code: 'moved', list: { id: '13', name: 'Our house' } }] }
      const hidden: Gift = { ...aGift, id: '61', title: 'A mug', can_acknowledge: true, flags: [{ code: 'moved', list: null }] }
      const fetch = serve({ ...giving(visible, hidden), 'POST /api/v2/gifts/60/acknowledge': { status: 200, body: aGift } })
      open('/giving')

      expect(await screen.findByText('Moved to Our house.')).toBeInTheDocument()
      expect(screen.getByText('Moved to another list.')).toBeInTheDocument()
      expect(screen.getByText(/You can't open that list\./)).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Got it, moved: A kettle' }))
      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['moved'] }))
    })

    test('a gift whose recipients changed says who it is for now, or only that they changed', async () => {
      const user = userEvent.setup()
      const dad = { id: '10', name: 'Dad', avatar: null }
      const named: Gift = { ...aGift, can_acknowledge: true, flags: [{ code: 'recipients_changed', old_recipients: [mom], new_recipients: [mom, dad] }] }
      const unnamed: Gift = { ...aGift, id: '61', title: 'A mug', can_acknowledge: true, flags: [{ code: 'recipients_changed', old_recipients: null, new_recipients: null }] }
      const fetch = serve({ ...giving(named, unnamed), 'POST /api/v2/gifts/60/acknowledge': { status: 200, body: aGift } })
      open('/giving')

      expect(await screen.findByText('Now for Mom and Dad.')).toBeInTheDocument()
      expect(screen.getByText('The recipients changed.')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Got it, recipients: A kettle' }))
      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['recipients_changed'] }))
    })

    test('a restored gift says who says they did not get it, or only that it was marked as not received', async () => {
      const user = userEvent.setup()
      const named: Gift = { ...aGift, can_acknowledge: true, flags: [{ code: 'restored', by: mom }, { code: 'removed' }] }
      const unnamed: Gift = { ...aGift, id: '61', title: 'A mug', can_acknowledge: true, flags: [{ code: 'restored', by: null }] }
      const fetch = serve({ ...giving(named, unnamed), 'POST /api/v2/gifts/60/acknowledge': { status: 200, body: aGift } })
      open('/giving')

      expect(await screen.findByText("Mom says they didn't get this.")).toBeInTheDocument()
      expect(screen.getByText('Marked as not received.')).toBeInTheDocument()
      expect(screen.getByText('Removed from the list.')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Got it, restored: A kettle' }))
      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['restored'] }))
    })

    test('a gift that moved to another occasion names it, or says it is out of sight', async () => {
      const user = userEvent.setup()
      const moved: Gift = { ...aGift, can_acknowledge: true, flags: [{ code: 'occasion_moved' }] }
      const hidden: Gift = { ...aGift, id: '61', title: 'A mug', occasion: null, occasion_name: null, can_acknowledge: true, flags: [{ code: 'occasion_moved' }] }
      const fetch = serve({ ...giving(moved, hidden), 'POST /api/v2/gifts/60/acknowledge': { status: 200, body: aGift } })
      open('/giving')

      expect(await screen.findByText('Moved to First Advent.')).toBeInTheDocument()
      expect(screen.getByText('Moved to another occasion.')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Got it, occasion: A kettle' }))
      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['occasion_moved'] }))
    })

    test('a gift that is no longer shared says so, and Got it clears the flag', async () => {
      const user = userEvent.setup()
      const unshared: Gift = { ...aGift, can_acknowledge: true, flags: [{ code: 'no_longer_shared' }] }
      const fetch = serve({ ...giving(unshared), 'POST /api/v2/gifts/60/acknowledge': { status: 200, body: aGift } })
      open('/giving')

      expect(await screen.findByText('No longer shared with you.')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Got it, no longer shared: A kettle' }))
      await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/gifts/60/acknowledge')).toEqual({ codes: ['no_longer_shared'] }))
    })

    test('says why an answer was refused', async () => {
      const user = userEvent.setup()
      serve({ ...giving(removed), 'POST /api/v2/gifts/60/acknowledge': problem(403, 'forbidden', 'This gift is given, so nobody changes it.') })
      open('/giving')

      await user.click(await screen.findByRole('button', { name: 'Keep it: A kettle' }))

      expect(await screen.findByRole('alert')).toHaveTextContent('This gift is given, so nobody changes it.')
    })
  })

  test("I won't get this opens the screen before it, and nothing is dropped at once", async () => {
    const fetch = serve(giving(aGift))
    open('/giving')

    const link = await screen.findByRole('link', { name: "I won't get this: A kettle" })
    expect(link).toHaveAttribute('href', '/gifts/60/unclaim')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/gifts/60', expect.objectContaining({ method: 'DELETE' }))
  })
})

describe("the screen before I won't get this", () => {
  test('says what happens, unclaims on the button, and returns to Giving', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...giving(aGift), 'GET /api/v2/gifts/60': { status: 200, body: aGift }, 'DELETE /api/v2/gifts/60': { status: 204 } })
    open('/gifts/60/unclaim')

    expect(await heading('Not getting this?')).toBeInTheDocument()
    expect(screen.getByText('A kettle comes out of Giving.')).toBeInTheDocument()
    expect(screen.getByText('Anyone else can get it now.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/giving#gift-60')
    await user.click(screen.getByRole('button', { name: "I won't get this" }))

    await waitFor(() => expect(window.location.pathname).toBe('/giving'))
    expect(fetch).toHaveBeenCalledWith('/api/v2/gifts/60', expect.objectContaining({ method: 'DELETE' }))
  })

  test('shows the missing-page message for a gift that is gone', async () => {
    serve({ ...asAda, 'GET /api/v2/gifts/60': problem(404, 'not_found') })
    open('/gifts/60/unclaim')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
