'use strict';

const assert = require('assert');
const { scanSource, scanFunctionBody } = require('../lib/scanner');

function run() {
  console.log('--- Test Suite: React Functional Components & Nested Handlers ---');

  // TS-SCAN-FE-01: React arrow component with nested async handlers and callbacks
  const code1 = `
import React, { useState } from 'react';

export const LoginPage = (props: LoginPageProps) => {
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    await authService.login(e.target.email.value);
  };

  const handleGoogleLogin = async () => {
    await authService.socialLogin('google');
  };

  function handleReset() {
    setLoading(false);
  }

  return (
    <form onSubmit={handleSubmit}>
      <button onClick={handleGoogleLogin}>Google</button>
      <button type="reset" onClick={handleReset}>Reset</button>
    </form>
  );
};
`;
  const scan1 = scanSource(code1);
  assert.strictEqual(scan1.topLevelFunctions.length, 4, 'Should extract LoginPage and 3 nested handlers');

  const loginPage = scan1.topLevelFunctions.find(f => f.name === 'LoginPage');
  assert(loginPage, 'LoginPage must be registered');
  assert.strictEqual(loginPage.localName, 'LoginPage');
  assert.strictEqual(loginPage.parentScope, '');
  assert.strictEqual(code1[loginPage.bodyEnd], '}');

  const handleSubmit = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.handleSubmit');
  assert(handleSubmit, 'LoginPage.handleSubmit must be registered');
  assert.strictEqual(handleSubmit.localName, 'handleSubmit');
  assert.strictEqual(handleSubmit.parentScope, 'LoginPage');
  assert.strictEqual(handleSubmit.isAsync, true);
  assert.strictEqual(code1[handleSubmit.bodyEnd], '}');
  assert(code1.substring(handleSubmit.bodyStart, handleSubmit.bodyEnd).includes('authService.login'));

  const handleGoogle = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.handleGoogleLogin');
  assert(handleGoogle, 'LoginPage.handleGoogleLogin must be registered');
  assert.strictEqual(handleGoogle.localName, 'handleGoogleLogin');
  assert.strictEqual(handleGoogle.parentScope, 'LoginPage');
  assert.strictEqual(handleGoogle.isAsync, true);

  const handleReset = scan1.topLevelFunctions.find(f => f.name === 'LoginPage.handleReset');
  assert(handleReset, 'LoginPage.handleReset must be registered');
  assert.strictEqual(handleReset.localName, 'handleReset');
  assert.strictEqual(handleReset.parentScope, 'LoginPage');
  assert.strictEqual(handleReset.isAsync, false);

  // TS-SCAN-FE-02: Standard function component with useCallback and memoized callbacks
  const code2 = `
export default function RegisterForm() {
  const onFormSubmit = useCallback(async (data: FormData) => {
    await submitForm(data);
  }, []);

  const onValidate = (field: string) => {
    return field.length > 0;
  };

  return <div />;
}
`;
  const scan2 = scanSource(code2);
  assert.strictEqual(scan2.topLevelFunctions.length, 3, 'Should extract RegisterForm and 2 handlers');
  const regForm = scan2.topLevelFunctions.find(f => f.name === 'RegisterForm');
  assert(regForm, 'RegisterForm must be registered');
  const onFormSubmit = scan2.topLevelFunctions.find(f => f.name === 'RegisterForm.onFormSubmit');
  assert(onFormSubmit, 'RegisterForm.onFormSubmit must be registered');
  assert.strictEqual(onFormSubmit.isAsync, true);
  assert.strictEqual(code2[onFormSubmit.bodyEnd], '}');

  // TS-SCAN-FE-03: React.memo and forwardRef wrapped functional components
  const code3 = `
export const MemoizedCard = React.memo((props: CardProps) => {
  const onCardClick = () => {
    props.onClick();
  };
  return <div onClick={onCardClick} />;
});

export const CustomInput = forwardRef<HTMLInputElement, InputProps>((props, ref) => {
  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    props.onChange(e.target.value);
  };
  return <input ref={ref} onChange={handleInputChange} />;
});
`;
  const scan3 = scanSource(code3);
  assert.strictEqual(scan3.topLevelFunctions.length, 4, 'Should extract 2 wrapped components and their handlers');
  assert(scan3.topLevelFunctions.some(f => f.name === 'MemoizedCard'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'MemoizedCard.onCardClick'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'CustomInput'));
  assert(scan3.topLevelFunctions.some(f => f.name === 'CustomInput.handleInputChange'));

  // TS-SCAN-FE-04: Deep 3-level nesting (Component -> Handler -> Inner Callback -> Helper)
  const code4 = `
export const CheckoutPage = () => {
  const handleCheckout = async () => {
    const validateCart = () => {
      const checkStock = () => {
        return true;
      };
      return checkStock();
    };
    if (validateCart()) {
      await processPayment();
    }
  };
};
`;
  const scan4 = scanSource(code4);
  assert.strictEqual(scan4.topLevelFunctions.length, 4, 'Should extract 4 functions across 3 nesting levels');
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.handleCheckout'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.handleCheckout.validateCart'));
  assert(scan4.topLevelFunctions.some(f => f.name === 'CheckoutPage.handleCheckout.validateCart.checkStock'));

  // TS-SCAN-FE-05: Custom hooks with inner functions
  const code5 = `
export function useAuth() {
  const [user, setUser] = useState(null);

  const login = useCallback(async (credentials: Credentials) => {
    const res = await api.post('/login', credentials);
    setUser(res.data);
  }, []);

  const logout = async () => {
    await api.post('/logout');
    setUser(null);
  };

  return { user, login, logout };
}
`;
  const scan5 = scanSource(code5);
  assert.strictEqual(scan5.topLevelFunctions.length, 3, 'Should extract useAuth and 2 methods');
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth'));
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth.login'));
  assert(scan5.topLevelFunctions.some(f => f.name === 'useAuth.logout'));

  // TS-SCAN-FE-06: Direct unit test for scanFunctionBody helper
  const rawBody = `
    const onClick = () => {
      console.log('clicked');
    };
    function onDismiss() {
      close();
    }
  `;
  const nested = scanFunctionBody(rawBody, 0, rawBody.length, 'ModalView');
  assert.strictEqual(nested.length, 2);
  assert.strictEqual(nested[0].name, 'ModalView.onClick');
  assert.strictEqual(nested[1].name, 'ModalView.onDismiss');

  console.log('✓ All React Functional Components & Nested Handlers tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
