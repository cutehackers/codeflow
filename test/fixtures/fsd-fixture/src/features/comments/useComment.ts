import { useState } from 'react';
import { feedApi } from '../../shared/api/client';

export function useComment(postId: string) {
  const [loading, setLoading] = useState(false);

  const submitComment = async (text: string) => {
    setLoading(true);
    try {
      await feedApi.posts.comment(postId, text);
    } finally {
      setLoading(false);
    }
  };

  return { loading, submitComment };
}
