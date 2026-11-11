import { RateLimitedError, type Passkey } from './api'
import { request } from './http'

const api = '/api/v2'

/** Why adding a passkey failed, in words for the user. */
export class PasskeyError extends Error {}

/** Every sign-in that did not work, whatever the reason. The page says nothing about why. */
export class PasskeyLoginError extends Error {}

/** True when this browser can create passkeys. */
export function passkeysSupported(): boolean {
  return (
    typeof PublicKeyCredential !== 'undefined' &&
    'parseCreationOptionsFromJSON' in PublicKeyCredential &&
    !!navigator.credentials?.create
  )
}

/**
 * Adds a passkey to the signed-in user: asks the server for options, asks the
 * browser's authenticator to create the credential, and sends the answer back.
 * Throws PasskeyError with a message to show.
 */
export async function addPasskey(userId: string, name: string): Promise<Passkey> {
  if (!passkeysSupported()) throw new PasskeyError("This browser can't add passkeys.")

  const begin = await request(`${api}/users/${userId}/passkeys/registration`, { method: 'POST' })
  if (!begin.ok) throw new Error(`begin passkey failed: ${begin.status}`)
  const { publicKey } = await begin.json()

  let credential: Credential | null
  try {
    credential = await navigator.credentials.create({
      publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(publicKey),
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'NotAllowedError') {
      throw new PasskeyError("The passkey wasn't added: the prompt was cancelled or timed out.")
    }
    if (e instanceof DOMException && e.name === 'InvalidStateError') {
      throw new PasskeyError('This device already has a passkey for Wishbone.')
    }
    throw new PasskeyError("This device couldn't create a passkey. Try another device.")
  }
  if (!credential) throw new PasskeyError("The passkey wasn't added.")

  const finish = await request(`${api}/users/${userId}/passkeys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, credential: (credential as PublicKeyCredential).toJSON() }),
  })
  if (!finish.ok) {
    const body = await finish.json().catch(() => null)
    const detail = typeof body?.detail === 'string' ? body.detail : ''
    if (finish.status === 400 || finish.status === 409) {
      throw new PasskeyError(detail || "The passkey wasn't added. Try again.")
    }
    throw new Error(`finish passkey failed: ${finish.status}`)
  }
  return finish.json()
}

async function rateLimited(res: Response): Promise<RateLimitedError> {
  const body = await res.json().catch(() => null)
  return new RateLimitedError(body?.detail ?? 'Too many attempts. Wait a minute and try again.')
}

/**
 * Signs in with a passkey: asks the server for options, asks the browser to
 * find a discoverable credential, and sends the answer back. The server sets
 * the session cookie. Throws RateLimitedError when the server limits attempts,
 * and PasskeyLoginError for every other failure.
 */
export async function signInWithPasskey(): Promise<void> {
  if (
    typeof PublicKeyCredential === 'undefined' ||
    !('parseRequestOptionsFromJSON' in PublicKeyCredential) ||
    !navigator.credentials?.get
  ) {
    throw new PasskeyLoginError()
  }

  const begin = await request(`${api}/login/passkey/options`, { method: 'POST' })
  if (begin.status === 429) throw await rateLimited(begin)
  if (!begin.ok) throw new PasskeyLoginError()
  const { publicKey } = await begin.json()

  let credential: Credential | null
  try {
    credential = await navigator.credentials.get({
      publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(publicKey),
    })
  } catch {
    throw new PasskeyLoginError()
  }
  if (!credential) throw new PasskeyLoginError()

  const finish = await request(`${api}/login/passkey`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ credential: (credential as PublicKeyCredential).toJSON() }),
  })
  if (finish.status === 429) throw await rateLimited(finish)
  if (!finish.ok) throw new PasskeyLoginError()
}
